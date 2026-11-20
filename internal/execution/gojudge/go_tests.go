package gojudge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"strings"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/gotestjob"
)

func (e *Executor) executeCompiledGoTests(ctx context.Context, command execution.ExecutionCommand, req Request) (execution.ExecutionResult, error) {
	input := req.Cmd[0].CopyIn
	for _, name := range []string{gotestjob.BundleName, gotestjob.RunBinaryName} {
		if _, exists := input[name]; exists {
			return execution.ExecutionResult{}, fmt.Errorf("%w: reserved Go test filename", execution.ErrInvalidCommandPayload)
		}
	}

	compileCtx, stopCompile := context.WithTimeout(ctx, goCompileBudget)
	build := goBuildCommand("/usr/local/bin/sandbox-go-test-compile")
	build.RuntimeLimits.StdoutBytes = min(build.RuntimeLimits.StdoutBytes, command.RuntimeLimits.StdoutBytes)
	build.RuntimeLimits.StderrBytes = min(build.RuntimeLimits.StderrBytes, command.RuntimeLimits.StderrBytes)
	data, verdict, err := e.buildGoArtifact(compileCtx, req.RequestID, build, input, gotestjob.BundleName, gotestjob.MaxBundleBytes)
	stopCompile()
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "Go test compiler request")
	}
	if verdict != nil {
		return *verdict, nil
	}
	packages, err := readGoTestBundle(data)
	if err != nil {
		return protocolErrorResult(), nil
	}

	runner, closeRunner := e.client.fileSession()
	defer closeRunner()
	runner.httpClient.Timeout = 0
	runBudget := time.Duration(command.RuntimeLimits.WallMS)*time.Millisecond + goRunHTTPReserve
	runCtx, stopRun := context.WithTimeout(ctx, runBudget)
	defer stopRun()
	response, err := runGoTestPackages(runCtx, runner, req.RequestID, req.Cmd[0], packages)
	if errors.Is(err, ErrProtocol) {
		return protocolErrorResult(), nil
	}
	if err != nil {
		return execution.ExecutionResult{}, phaseError(ctx, err, "Go test runner request")
	}
	result := ExecutionResultFromResponse(command, response)
	if result.Status == contracts.StatusCompileError {
		result = withGoJudgeError(result, contracts.StatusRuntimeError, "go_test_error", "Go test exited with non-zero status", false)
	}
	return result, nil
}

func runGoTestPackages(ctx context.Context, client *Client, requestID string, cmd Command, packages []goTestPackage) (Result, error) {
	result := Result{Status: StatusAccepted}
	var stdout, stderr strings.Builder
	stdoutLimit, stderrLimit := cmd.Files[1].Max, cmd.Files[2].Max
	cpuLeft, wallLeft := cmd.CPULimit, cmd.ClockLimit
	for _, pkg := range packages {
		if cpuLeft <= 0 || wallLeft <= 0 {
			result.Status = StatusTimeLimitExceeded
			break
		}
		run := cmd
		run.CPULimit, run.ClockLimit = cpuLeft, wallLeft
		run.Files = append([]FileDescriptor(nil), cmd.Files...)
		run.Files[1].Max = max(1, stdoutLimit-stdout.Len())
		run.Files[2].Max = max(1, stderrLimit-stderr.Len())
		part, err := runGoTestPackage(ctx, client, requestID, run, pkg)
		if err != nil {
			return Result{}, err
		}
		if result.Time > math.MaxInt64-part.Time || result.RunTime > math.MaxInt64-part.RunTime {
			return Result{}, ErrProtocol
		}
		result.Time += part.Time
		result.RunTime += part.RunTime
		result.Memory = max(result.Memory, part.Memory)
		cpuLeft -= part.Time
		wallLeft -= part.RunTime
		truncatedOut := appendLimited(&stdout, part.Files[stdoutName], stdoutLimit)
		truncatedErr := appendLimited(&stderr, part.Files[stderrName], stderrLimit)
		if part.Status != StatusAccepted {
			result.Status, result.ExitStatus = part.Status, part.ExitStatus
			result.Error, result.FileError = part.Error, part.FileError
		}
		switch part.Status {
		case StatusAccepted, StatusNonzeroExitStatus:
			if cpuLeft < 0 || wallLeft < 0 {
				result.Status = StatusTimeLimitExceeded
			} else if truncatedOut || truncatedErr {
				result.Status = StatusOutputLimitExceeded
			} else {
				continue
			}
		}
		break
	}
	result.Files = map[string]string{stdoutName: stdout.String(), stderrName: stderr.String()}
	return result, nil
}

func runGoTestPackage(ctx context.Context, client *Client, requestID string, cmd Command, pkg goTestPackage) (Result, error) {
	id, err := client.putArtifact(ctx, pkg.binary)
	if err != nil {
		return Result{}, err
	}
	defer client.deleteArtifact(ctx, id)
	cmd.CopyIn = maps.Clone(cmd.CopyIn)
	cmd.CopyIn[gotestjob.RunBinaryName] = MemoryFile{FileID: id}
	cmd.Args = []string{"/usr/local/bin/sandbox-go-test-run", pkg.Dir, pkg.ImportPath}
	results, err := client.Run(ctx, Request{RequestID: requestID, Cmd: []Command{cmd}})
	var httpErr HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return Result{}, fmt.Errorf("%w: Go test binary unavailable in runner", ErrInfrastructure)
	}
	if err != nil {
		return Result{}, err
	}
	if ValidateResponse(results) != nil {
		return Result{}, ErrProtocol
	}
	if results[0].Status == StatusNonzeroExitStatus && results[0].ExitStatus == 125 {
		return Result{}, fmt.Errorf("%w: Go test launcher failed", ErrInfrastructure)
	}
	return results[0], nil
}

func appendLimited(dst *strings.Builder, value string, limit int) bool {
	left := max(0, limit-dst.Len())
	truncated := len(value) > left
	dst.WriteString(value[:min(len(value), left)])
	return truncated
}
