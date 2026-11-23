package gojudge

import (
	"context"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages/cpp"
)

const (
	cppCompileWall   = 15 * time.Second
	cppCompileBudget = cppCompileWall + 5*time.Second
)

func (e *Executor) executeCompiledCPP(ctx context.Context, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	if !execution.IsExecutionMode(command) || len(command.CompileArgv) == 0 || command.CompileArgv[0] != cpp.CompilerPath {
		return execution.ExecutionResult{}, fmt.Errorf("%w: missing C++ compile command", execution.ErrInvalidCommandPayload)
	}
	input := run.Cmd[0].CopyIn
	if _, exists := input[cpp.ArtifactName]; exists {
		return execution.ExecutionResult{}, fmt.Errorf("%w: reserved build filename", execution.ErrInvalidCommandPayload)
	}
	compileCtx, cancel := context.WithTimeout(ctx, cppCompileBudget)
	defer cancel()
	binary, verdict, err := e.buildCPP(compileCtx, run.RequestID, command.CompileArgv, input)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "C++ compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	if limit := command.RuntimeLimits.WorkspaceBytes; limit > 0 && int64(len(binary)) > limit {
		result := withGoJudgeError(execution.ExecutionResult{}, contracts.StatusOutputLimitExceeded,
			"cpp_artifact_too_large", "C++ executable exceeds the workspace limit", false)
		return result, nil
	}

	return e.runCompiledBinary(ctx, compileCtx, command, run, cpp.ArtifactName, binary, nil)
}

func (e *Executor) buildCPP(ctx context.Context, id string, argv []string, input map[string]MemoryFile) ([]byte, *execution.ExecutionResult, error) {
	build := execution.ExecutionCommand{
		Argv:        argv,
		Environment: map[string]string{"LC_ALL": "C"},
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 8000, WallMS: int(cppCompileWall / time.Millisecond), MemoryBytes: 512 << 20,
			PIDs: 32, StdoutBytes: 32768, StderrBytes: 32768,
		},
	}
	cmd := baseCommand(build, input, nil, maxArtifactBytes, "")
	cmd.CopyOutCached = []string{optionalCopyOut(cpp.ArtifactName)}
	compiler, closeCompiler := e.compiler.fileSession()
	defer closeCompiler()
	res, err := compiler.Run(ctx, Request{RequestID: id, Cmd: []Command{cmd}})
	if err != nil {
		return nil, nil, err
	}
	if ValidateResponse(res) != nil {
		return nil, nil, ErrProtocol
	}
	artifactID := res[0].FileIDs[cpp.ArtifactName]
	defer compiler.deleteArtifact(ctx, artifactID)
	switch res[0].Status {
	case StatusAccepted:
		binary, err := compiler.getArtifact(ctx, artifactID)
		return binary, nil, err
	case StatusFileError, StatusInternalError:
		return nil, nil, fmt.Errorf("%w: C++ compiler could not run", ErrInfrastructure)
	default:
		verdict := ExecutionResultFromResponse(build, res[0])
		if res[0].Status == StatusNonzeroExitStatus {
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, "cpp_compile_error", "C++ compilation failed", false)
		}
		return nil, &verdict, nil
	}
}
