package gojudge

import (
	"context"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/tsjob"
)

const (
	tsCompileWall   = 12 * time.Second
	tsCompileBudget = tsCompileWall + 5*time.Second
)

func (e *Executor) executeCompiledTypeScript(ctx context.Context, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	entry, ok := tsjob.JSPath(command.Entrypoint)
	if !ok || !execution.IsExecutionMode(command) || len(command.CompileArgv) < 2 ||
		command.CompileArgv[0] != tsjob.CompilerPath || len(command.Argv) != 3 || command.Argv[0] != tsjob.RunnerPath || command.Argv[1] != entry {
		return execution.ExecutionResult{}, fmt.Errorf("%w: invalid TypeScript compile command", execution.ErrInvalidCommandPayload)
	}
	compileCtx, stop := context.WithTimeout(ctx, tsCompileBudget)
	defer stop()
	input := run.Cmd[0].CopyIn
	bundle, verdict, err := e.buildTypeScript(compileCtx, command, run.RequestID, input)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "TypeScript compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	files, err := tsjob.ReadBundle(bundle, entry)
	if err != nil || len(files) != len(input) {
		return execution.ExecutionResult{}, ErrProtocol
	}
	// The artifact and its unpacked files coexist in each case's workspace.
	workspace := int64(len(bundle))
	for _, file := range files {
		source := file.Path[:len(file.Path)-len(".js")] + ".ts"
		if _, ok := input[source]; !ok {
			return execution.ExecutionResult{}, ErrProtocol
		}
		workspace += int64(len(file.Content))
	}
	if limit := command.RuntimeLimits.WorkspaceBytes; limit > 0 && workspace > limit {
		return tsArtifactLimit(execution.ExecutionResult{}), nil
	}
	return e.runCompiledArtifact(ctx, compileCtx, command, run, tsjob.BundleName, command.Argv, nil,
		func(ctx context.Context, runner *Client) (string, error) {
			return runner.putNamedArtifact(ctx, tsjob.BundleName, bundle)
		})
}

func (e *Executor) buildTypeScript(ctx context.Context, command execution.ExecutionCommand, id string,
	input map[string]MemoryFile,
) ([]byte, *execution.ExecutionResult, error) {
	build := execution.ExecutionCommand{
		Argv: command.CompileArgv,
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 8000, WallMS: int(tsCompileWall / time.Millisecond), MemoryBytes: 512 << 20, PIDs: 32,
			StdoutBytes: min(32768, command.RuntimeLimits.StdoutBytes), StderrBytes: min(32768, command.RuntimeLimits.StderrBytes),
		},
	}
	cmd := baseCommand(build, input, nil, tsjob.MaxBundleBytes, "")
	cmd.CopyOutCached = []string{optionalCopyOut(tsjob.BundleName)}
	compiler, closeCompiler := e.compiler.fileSession()
	defer closeCompiler()
	results, err := compiler.Run(ctx, Request{RequestID: id, Cmd: []Command{cmd}})
	if err != nil {
		return nil, nil, err
	}
	if ValidateResponse(results) != nil {
		return nil, nil, ErrProtocol
	}
	result := results[0]
	artifactID := result.FileIDs[tsjob.BundleName]
	defer compiler.deleteArtifact(ctx, artifactID)
	switch result.Status {
	case StatusAccepted:
		data, err := compiler.readArtifact(ctx, artifactID, tsjob.MaxBundleBytes)
		return data, nil, err
	case StatusFileError, StatusInternalError:
		return nil, nil, fmt.Errorf("%w: TypeScript compiler could not run", ErrInfrastructure)
	default:
		verdict := ExecutionResultFromResponse(build, result)
		if result.Status != StatusNonzeroExitStatus {
			return nil, &verdict, nil
		}
		switch result.ExitStatus {
		case tsjob.CompileError:
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, "typescript_compile_error", "TypeScript compilation failed", false)
		case tsjob.ArtifactLimit:
			verdict = tsArtifactLimit(verdict)
		default:
			return nil, nil, fmt.Errorf("%w: TypeScript compiler could not prepare output", ErrInfrastructure)
		}
		return nil, &verdict, nil
	}
}

func tsArtifactLimit(result execution.ExecutionResult) execution.ExecutionResult {
	return withGoJudgeError(result, contracts.StatusOutputLimitExceeded, "typescript_artifact_too_large", "TypeScript output exceeds the workspace limit", false)
}
