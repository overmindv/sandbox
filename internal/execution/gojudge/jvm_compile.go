package gojudge

import (
	"context"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/javaclass"
	"overmindv/sandbox/internal/languages/jvm"
)

const javaCompileWall = 15 * time.Second

type jvmCompileLimits struct {
	cpuMS, memoryMiB int
	wall             time.Duration
}

func compileLimitsForJVM(adapter jvm.Adapter) jvmCompileLimits {
	if adapter.Name() == jvm.KotlinName {
		return jvmCompileLimits{cpuMS: 18000, memoryMiB: 768, wall: 30 * time.Second}
	}
	return jvmCompileLimits{cpuMS: 8000, memoryMiB: 512, wall: javaCompileWall}
}

func (l jvmCompileLimits) budget() time.Duration {
	return l.wall + 5*time.Second
}

func (e *Executor) executeCompiledJVM(ctx context.Context, adapter jvm.Adapter, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	main, ok := adapter.MainClass(command.Entrypoint)
	if !ok || !execution.IsExecutionMode(command) || len(command.CompileArgv) < 3 ||
		command.CompileArgv[0] != adapter.CompilerPath() || command.CompileArgv[1] != command.Entrypoint {
		return execution.ExecutionResult{}, fmt.Errorf("%w: invalid %s compile command", execution.ErrInvalidCommandPayload, adapter.Label())
	}
	compileCtx, cancel := context.WithTimeout(ctx, compileLimitsForJVM(adapter).budget())
	defer cancel()
	archive, verdict, err := e.buildJVM(compileCtx, adapter, command, run.RequestID, run.Cmd[0].CopyIn, main)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, adapter.Label()+" compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	if limit := command.RuntimeLimits.WorkspaceBytes; limit > 0 && int64(len(archive)) > limit {
		return jvmArchiveLimitResult(adapter, execution.ExecutionResult{}), nil
	}
	return e.runCompiledArtifact(ctx, compileCtx, command, run, adapter.ArchiveName(), command.Argv, nil,
		func(ctx context.Context, runner *Client) (string, error) {
			return runner.putNamedArtifact(ctx, adapter.ArchiveName(), archive)
		})
}

func (e *Executor) buildJVM(ctx context.Context, adapter jvm.Adapter, command execution.ExecutionCommand, id string,
	input map[string]MemoryFile, main string,
) ([]byte, *execution.ExecutionResult, error) {
	limits := compileLimitsForJVM(adapter)
	build := execution.ExecutionCommand{
		Argv:        command.CompileArgv,
		Environment: map[string]string{"LC_ALL": "C.UTF-8"},
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: limits.cpuMS, WallMS: int(limits.wall / time.Millisecond), MemoryBytes: int64(limits.memoryMiB) << 20,
			PIDs: 48, StdoutBytes: min(32768, command.RuntimeLimits.StdoutBytes), StderrBytes: min(32768, command.RuntimeLimits.StderrBytes),
		},
	}
	cmd := baseCommand(build, input, nil, javaclass.MaxBytes, "")
	cmd.CopyOutCached = []string{optionalCopyOut(adapter.ArchiveName())}
	compiler, closeCompiler := e.compiler.fileSession()
	defer closeCompiler()
	res, err := compiler.Run(ctx, Request{RequestID: id, Cmd: []Command{cmd}})
	if err != nil {
		return nil, nil, err
	}
	if ValidateResponse(res) != nil {
		return nil, nil, ErrProtocol
	}
	artifactID := res[0].FileIDs[adapter.ArchiveName()]
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
		return nil, nil, fmt.Errorf("%w: %s compiler could not run", ErrInfrastructure, adapter.Label())
	default:
		verdict := ExecutionResultFromResponse(build, res[0])
		if res[0].Status != StatusNonzeroExitStatus {
			return nil, &verdict, nil
		}
		switch res[0].ExitStatus {
		case javaclass.CompileErrorExit:
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, adapter.Name()+"_compile_error", adapter.Label()+" compilation failed", false)
		case javaclass.ArtifactLimitExit:
			verdict = jvmArchiveLimitResult(adapter, verdict)
		default:
			return nil, nil, fmt.Errorf("%w: %s compiler could not prepare class archive", ErrInfrastructure, adapter.Label())
		}
		return nil, &verdict, nil
	}
}

func jvmArchiveLimitResult(adapter jvm.Adapter, result execution.ExecutionResult) execution.ExecutionResult {
	return withGoJudgeError(result, contracts.StatusOutputLimitExceeded, adapter.Name()+"_artifact_too_large", adapter.Label()+" classes exceed the workspace limit", false)
}
