package gojudge

import (
	"context"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/javaclass"
)

const (
	javaCompileWall   = 15 * time.Second
	javaCompileBudget = javaCompileWall + 5*time.Second
)

func (e *Executor) executeCompiledJava(ctx context.Context, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	main, ok := javaclass.MainClass(command.Entrypoint)
	if !ok || !execution.IsExecutionMode(command) || len(command.CompileArgv) < 3 ||
		command.CompileArgv[0] != javaclass.CompilerPath || command.CompileArgv[1] != command.Entrypoint {
		return execution.ExecutionResult{}, fmt.Errorf("%w: invalid Java compile command", execution.ErrInvalidCommandPayload)
	}
	compileCtx, cancel := context.WithTimeout(ctx, javaCompileBudget)
	defer cancel()
	archive, verdict, err := e.buildJava(compileCtx, command, run.RequestID, run.Cmd[0].CopyIn, main)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "Java compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	if limit := command.RuntimeLimits.WorkspaceBytes; limit > 0 && int64(len(archive)) > limit {
		return javaArchiveLimitResult(execution.ExecutionResult{}), nil
	}
	return e.runCompiledArtifact(ctx, compileCtx, command, run, javaclass.ArchiveName, command.Argv, nil,
		func(ctx context.Context, runner *Client) (string, error) {
			return runner.putNamedArtifact(ctx, javaclass.ArchiveName, archive)
		})
}

func (e *Executor) buildJava(ctx context.Context, command execution.ExecutionCommand, id string,
	input map[string]MemoryFile, main string,
) ([]byte, *execution.ExecutionResult, error) {
	build := execution.ExecutionCommand{
		Argv:        command.CompileArgv,
		Environment: map[string]string{"LC_ALL": "C.UTF-8"},
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 8000, WallMS: int(javaCompileWall / time.Millisecond), MemoryBytes: 512 << 20,
			PIDs: 48, StdoutBytes: min(32768, command.RuntimeLimits.StdoutBytes), StderrBytes: min(32768, command.RuntimeLimits.StderrBytes),
		},
	}
	cmd := baseCommand(build, input, nil, javaclass.MaxBytes, "")
	cmd.CopyOutCached = []string{optionalCopyOut(javaclass.ArchiveName)}
	compiler, closeCompiler := e.compiler.fileSession()
	defer closeCompiler()
	res, err := compiler.Run(ctx, Request{RequestID: id, Cmd: []Command{cmd}})
	if err != nil {
		return nil, nil, err
	}
	if ValidateResponse(res) != nil {
		return nil, nil, ErrProtocol
	}
	artifactID := res[0].FileIDs[javaclass.ArchiveName]
	defer compiler.deleteArtifact(ctx, artifactID)
	switch res[0].Status {
	case StatusAccepted:
		data, err := compiler.readArtifact(ctx, artifactID, javaclass.MaxBytes)
		if err != nil {
			return nil, nil, err
		}
		if javaclass.ValidateArchive(data, main) != nil {
			return nil, nil, ErrProtocol
		}
		return data, nil, nil
	case StatusFileError, StatusInternalError:
		return nil, nil, fmt.Errorf("%w: Java compiler could not run", ErrInfrastructure)
	default:
		verdict := ExecutionResultFromResponse(build, res[0])
		if res[0].Status != StatusNonzeroExitStatus {
			return nil, &verdict, nil
		}
		switch res[0].ExitStatus {
		case javaclass.CompileErrorExit:
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, "java_compile_error", "Java compilation failed", false)
		case javaclass.ArtifactLimitExit:
			verdict = javaArchiveLimitResult(verdict)
		default:
			return nil, nil, fmt.Errorf("%w: Java compiler could not prepare class archive", ErrInfrastructure)
		}
		return nil, &verdict, nil
	}
}

func javaArchiveLimitResult(result execution.ExecutionResult) execution.ExecutionResult {
	return withGoJudgeError(result, contracts.StatusOutputLimitExceeded, "java_artifact_too_large", "Java classes exceed the workspace limit", false)
}
