package gojudge

import (
	"context"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages/native"
)

const (
	nativeCompileWall   = 15 * time.Second
	nativeCompileBudget = nativeCompileWall + 5*time.Second
)

func (e *Executor) executeCompiledNative(ctx context.Context, adapter native.Adapter, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	if !execution.IsExecutionMode(command) || len(command.CompileArgv) == 0 || command.CompileArgv[0] != adapter.CompilerPath() {
		return execution.ExecutionResult{}, fmt.Errorf("%w: missing %s compile command", execution.ErrInvalidCommandPayload, adapter.Label())
	}
	artifact := adapter.ArtifactName()
	input := run.Cmd[0].CopyIn
	if _, exists := input[artifact]; exists {
		return execution.ExecutionResult{}, fmt.Errorf("%w: reserved build filename", execution.ErrInvalidCommandPayload)
	}
	compileCtx, cancel := context.WithTimeout(ctx, nativeCompileBudget)
	defer cancel()
	binary, verdict, err := e.buildNative(compileCtx, adapter, run.RequestID, command.CompileArgv, input)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, adapter.Label()+" compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	if limit := command.RuntimeLimits.WorkspaceBytes; limit > 0 && int64(len(binary)) > limit {
		result := withGoJudgeError(execution.ExecutionResult{}, contracts.StatusOutputLimitExceeded,
			adapter.Name()+"_artifact_too_large", adapter.Label()+" executable exceeds the workspace limit", false)
		return result, nil
	}

	return e.runCompiledBinary(ctx, compileCtx, command, run, artifact, binary, nil)
}

func (e *Executor) buildNative(ctx context.Context, adapter native.Adapter, id string, argv []string, input map[string]MemoryFile) ([]byte, *execution.ExecutionResult, error) {
	build := execution.ExecutionCommand{
		Argv:        argv,
		Environment: map[string]string{"LC_ALL": "C"},
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 8000, WallMS: int(nativeCompileWall / time.Millisecond), MemoryBytes: 512 << 20,
			PIDs: 32, StdoutBytes: 32768, StderrBytes: 32768,
		},
	}
	cmd := baseCommand(build, input, nil, maxArtifactBytes, "")
	cmd.CopyOutCached = []string{optionalCopyOut(adapter.ArtifactName())}
	compiler, closeCompiler := e.compiler.fileSession()
	defer closeCompiler()
	res, err := compiler.Run(ctx, Request{RequestID: id, Cmd: []Command{cmd}})
	if err != nil {
		return nil, nil, err
	}
	if ValidateResponse(res) != nil {
		return nil, nil, ErrProtocol
	}
	artifactID := res[0].FileIDs[adapter.ArtifactName()]
	defer compiler.deleteArtifact(ctx, artifactID)
	switch res[0].Status {
	case StatusAccepted:
		binary, err := compiler.getArtifact(ctx, artifactID)
		return binary, nil, err
	case StatusFileError, StatusInternalError:
		return nil, nil, fmt.Errorf("%w: %s compiler could not run", ErrInfrastructure, adapter.Label())
	default:
		verdict := ExecutionResultFromResponse(build, res[0])
		if res[0].Status == StatusNonzeroExitStatus {
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, adapter.Name()+"_compile_error", adapter.Label()+" compilation failed", false)
		}
		return nil, &verdict, nil
	}
}
