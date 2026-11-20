package gojudge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	golang "overmindv/sandbox/internal/languages/go"
)

const (
	goArtifactName   = "_sandbox_go"
	goCompileWall    = 65 * time.Second
	goCompileBudget  = goCompileWall + 5*time.Second
	goRunHTTPReserve = 5 * time.Second
	goCleanupReserve = 3 * time.Second
)

func (e *Executor) BuildAllowance(command execution.ExecutionCommand) time.Duration {
	if e.compiler != nil && command.Adapter == golang.AdapterName && execution.IsExecutionMode(command) {
		return goCompileBudget + goRunHTTPReserve + goCleanupReserve
	}
	return 0
}

func (e *Executor) executeCompiledGo(ctx context.Context, command execution.ExecutionCommand, run Request) (execution.ExecutionResult, error) {
	input := run.Cmd[0].CopyIn
	if _, exists := input[goArtifactName]; exists {
		return execution.ExecutionResult{}, fmt.Errorf("%w: reserved build filename", execution.ErrInvalidCommandPayload)
	}

	compileCtx, cancel := context.WithTimeout(ctx, goCompileBudget)
	defer cancel()
	binary, verdict, err := e.buildGo(compileCtx, run.RequestID, input)
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "Go compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}

	runner, closeRunner := e.client.fileSession()
	defer closeRunner()
	// Upload, run and cleanup use their own context deadlines.
	runner.httpClient.Timeout = 0
	id, err := runner.putArtifact(compileCtx, binary)
	cancel()
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "Go artifact upload")
	}
	defer runner.deleteArtifact(ctx, id)

	entry := maps.Clone(input)
	entry[goArtifactName] = MemoryFile{FileID: id}
	for i := range run.Cmd {
		run.Cmd[i].Args = []string{"./" + goArtifactName}
		run.Cmd[i].CopyIn = entry
	}
	runBudget := time.Duration(command.RuntimeLimits.WallMS)*time.Millisecond + goRunHTTPReserve
	runCtx, stopRun := context.WithTimeout(ctx, runBudget)
	defer stopRun()
	result, err := executeRequest(runCtx, runner, command, run)
	var httpErr HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return execution.ExecutionResult{}, fmt.Errorf("%w: compiled artifact unavailable in runner", ErrInfrastructure)
	}
	return result, phaseError(ctx, err, "Go runner request")
}

func phaseError(parent context.Context, err error, phase string) error {
	if err != nil && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s timed out", ErrInfrastructure, phase)
	}
	return err
}

func (e *Executor) buildGo(ctx context.Context, id string, input map[string]MemoryFile) ([]byte, *execution.ExecutionResult, error) {
	build := execution.ExecutionCommand{
		Argv: []string{
			"/usr/local/go/bin/go", "build", "-buildmode=exe", "-mod=readonly", "-trimpath", "-p=2",
			"-ldflags=-s -w", "-o", goArtifactName, ".",
		},
		Environment: map[string]string{
			"CGO_ENABLED": "0",
			"GOCACHE":     "/tmp/go-build",
			"GOMODCACHE":  "/tmp/go-mod",
			"GOENV":       "off",
			"GOMAXPROCS":  "2",
			"GOPROXY":     "off",
			"GOTOOLCHAIN": "local",
			"GOWORK":      "off",
			"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		},
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 40000, WallMS: int(goCompileWall / time.Millisecond), MemoryBytes: 768 << 20,
			PIDs: 64, StdoutBytes: 32768, StderrBytes: 32768,
		},
	}
	cmd := baseCommand(build, input, nil, maxArtifactBytes, "")
	cmd.CopyOutCached = []string{goArtifactName}
	compiler, closeCompiler := e.compiler.fileSession()
	defer closeCompiler()
	res, err := compiler.Run(ctx, Request{RequestID: id, Cmd: []Command{cmd}})
	if err != nil {
		return nil, nil, err
	}
	if ValidateResponse(res) != nil {
		return nil, nil, ErrProtocol
	}
	artifactID := res[0].FileIDs[goArtifactName]
	if validFileID(artifactID) {
		defer compiler.deleteArtifact(ctx, artifactID)
	}
	if res[0].Status != StatusAccepted {
		verdict := ExecutionResultFromResponse(build, res[0])
		if res[0].Status == StatusNonzeroExitStatus {
			verdict = withGoJudgeError(verdict, contracts.StatusCompileError, "go_compile_error", "go build failed", false)
		}
		return nil, &verdict, nil
	}
	data, err := compiler.getArtifact(ctx, artifactID)
	return data, nil, err
}
