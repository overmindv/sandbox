package gojudge

import (
	"context"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/dotnetjob"
	"overmindv/sandbox/internal/execution"
)

const (
	csharpCompileWall   = 15 * time.Second
	csharpCompileBudget = csharpCompileWall + 5*time.Second
)

func (e *Executor) executeCompiledCSharp(ctx context.Context, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	if !execution.IsExecutionMode(command) || len(command.CompileArgv) < 2 || command.CompileArgv[0] != dotnetjob.CompilerPath {
		return execution.ExecutionResult{}, fmt.Errorf("%w: invalid C# compile command", execution.ErrInvalidCommandPayload)
	}
	compileCtx, stop := context.WithTimeout(ctx, csharpCompileBudget)
	defer stop()
	assembly, verdict, err := e.buildCSharp(compileCtx, command, run.RequestID, run.Cmd[0].CopyIn)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "C# compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	if limit := command.RuntimeLimits.WorkspaceBytes; limit > 0 && int64(len(assembly)) > limit {
		return csharpArtifactLimit(execution.ExecutionResult{}), nil
	}
	return e.runCompiledArtifact(ctx, compileCtx, command, run, dotnetjob.AssemblyName, command.Argv, nil,
		func(ctx context.Context, runner *Client) (string, error) {
			return runner.putNamedArtifact(ctx, dotnetjob.AssemblyName, assembly)
		})
}

func (e *Executor) buildCSharp(ctx context.Context, command execution.ExecutionCommand, id string,
	input map[string]MemoryFile,
) ([]byte, *execution.ExecutionResult, error) {
	build := execution.ExecutionCommand{
		Argv: command.CompileArgv,
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 8000, WallMS: int(csharpCompileWall / time.Millisecond), MemoryBytes: 512 << 20, PIDs: 32,
			StdoutBytes: min(32768, command.RuntimeLimits.StdoutBytes), StderrBytes: min(32768, command.RuntimeLimits.StderrBytes),
		},
	}
	cmd := baseCommand(build, input, nil, dotnetjob.MaxBytes, "")
	cmd.CopyOutCached = []string{optionalCopyOut(dotnetjob.AssemblyName)}
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
	artifactID := result.FileIDs[dotnetjob.AssemblyName]
	defer compiler.deleteArtifact(ctx, artifactID)
	switch result.Status {
	case StatusAccepted:
		data, err := compiler.readArtifact(ctx, artifactID, dotnetjob.MaxBytes)
		if err != nil {
			return nil, nil, err
		}
		if !dotnetjob.IsAssembly(data) {
			return nil, nil, ErrProtocol
		}
		return data, nil, nil
	case StatusFileError, StatusInternalError:
		return nil, nil, fmt.Errorf("%w: C# compiler could not run", ErrInfrastructure)
	default:
		verdict := ExecutionResultFromResponse(build, result)
		if result.Status != StatusNonzeroExitStatus {
			return nil, &verdict, nil
		}
		switch result.ExitStatus {
		case dotnetjob.CompileError:
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, "csharp_compile_error", "C# compilation failed", false)
		case dotnetjob.ArtifactLimit:
			verdict = csharpArtifactLimit(verdict)
		default:
			return nil, nil, fmt.Errorf("%w: C# compiler could not prepare output", ErrInfrastructure)
		}
		return nil, &verdict, nil
	}
}

func csharpArtifactLimit(result execution.ExecutionResult) execution.ExecutionResult {
	return withGoJudgeError(result, contracts.StatusOutputLimitExceeded, "csharp_artifact_too_large", "C# assembly exceeds the workspace limit", false)
}
