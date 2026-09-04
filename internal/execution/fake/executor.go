package fake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

type Executor struct {
	defaultStatus contracts.ExecutionStatus
	latency       time.Duration
	healthy       bool
}

func NewExecutor(cfg config.FakeExecutor) (*Executor, error) {
	status, err := normalizeStatus(cfg.DefaultStatus)
	if err != nil {
		return nil, err
	}
	if status == "" {
		status = contracts.StatusPassed
	}

	return &Executor{
		defaultStatus: status,
		latency:       time.Duration(cfg.LatencyMS) * time.Millisecond,
		healthy:       true,
	}, nil
}

func (e *Executor) Execute(ctx context.Context, command execution.ExecutionCommand) (execution.ExecutionResult, error) {
	if command.AttemptID == "" {
		return execution.ExecutionResult{}, execution.ErrInvalidCommandPayload
	}

	if e.latency > 0 {
		timer := time.NewTimer(e.latency)
		select {
		case <-ctx.Done():
			timer.Stop()
			return execution.ExecutionResult{}, ctx.Err()
		case <-timer.C:
		}
	}

	if execution.IsExecutionMode(command) {
		return fakeExecutionModeResult(command, e.defaultStatus), nil
	}

	status, err := e.statusFor(command)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	return resultForStatus(status, command), nil
}

// fakeExecutionModeResult имитирует исполнение по входным кейсам без реального рантайма:
// для каждого входа возвращается кейс с эхом входа в stdout и мета-данными.
func fakeExecutionModeResult(command execution.ExecutionCommand, defaultStatus contracts.ExecutionStatus) execution.ExecutionResult {
	caseStatus := contracts.StatusOK
	switch defaultStatus {
	case contracts.StatusTimeLimitExceeded:
		caseStatus = contracts.StatusTimeLimitExceeded
	case contracts.StatusMemoryLimitExceeded:
		caseStatus = contracts.StatusMemoryLimitExceeded
	case contracts.StatusFailed, contracts.StatusRuntimeError:
		caseStatus = contracts.StatusRuntimeError
	case contracts.StatusPassed, contracts.StatusOK, "":
		caseStatus = contracts.StatusOK
	}

	cases := make([]execution.ExecutionCaseResult, 0, len(command.Inputs))
	overall := contracts.StatusOK
	exitCode := 0
	for _, input := range command.Inputs {
		run := execution.ExecutionCaseResult{
			Stdout:          input,
			ExitCode:        &exitCode,
			CPUms:           1,
			MemoryPeakBytes: 8 * 1024 * 1024,
			DurationMS:      1,
			Status:          caseStatus,
		}
		if caseStatus != contracts.StatusOK {
			overall = caseStatus
		}
		cases = append(cases, run)
	}

	return execution.ExecutionResult{
		Status:          overall,
		Cases:           cases,
		MemoryPeakBytes: 8 * 1024 * 1024,
	}
}

func (e *Executor) Healthy() bool {
	return e != nil && e.healthy
}

func (e *Executor) statusFor(command execution.ExecutionCommand) (contracts.ExecutionStatus, error) {
	if value, ok := command.Metadata["fake_status"].(string); ok && value != "" {
		return normalizeStatus(value)
	}
	if value, ok := command.Metadata["fake_result"].(string); ok && value != "" {
		return normalizeStatus(value)
	}

	for _, file := range command.Files {
		status, ok, err := statusFromText(string(file.Content))
		if err != nil || ok {
			return status, err
		}
	}

	return e.defaultStatus, nil
}

func statusFromText(text string) (contracts.ExecutionStatus, bool, error) {
	normalized := strings.ToLower(text)
	prefixes := []string{
		"fake_status=",
		"fake_status:",
		"fake-status=",
		"fake-status:",
		"fake:status=",
		"fake:status:",
		"fake_result=",
		"fake_result:",
	}

	for _, prefix := range prefixes {
		index := strings.Index(normalized, prefix)
		if index < 0 {
			continue
		}
		value := normalized[index+len(prefix):]
		value = strings.TrimLeft(value, " \t'\"")
		parts := strings.FieldsFunc(value, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ';' || r == ',' || r == '\'' || r == '"'
		})
		if len(parts) == 0 {
			return "", true, fmt.Errorf("%w: empty marker", execution.ErrUnsupportedFakeStatus)
		}
		value = parts[0]
		status, err := normalizeStatus(value)
		return status, true, err
	}
	return "", false, nil
}

func normalizeStatus(value string) (contracts.ExecutionStatus, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "success", "passed", "pass", "ok":
		return contracts.StatusPassed, nil
	case "fail", "failed", "wrong_answer", "wa":
		return contracts.StatusFailed, nil
	case "tle", "timeout", "time_limit_exceeded":
		return contracts.StatusTimeLimitExceeded, nil
	case "mle", "memory", "memory_limit_exceeded":
		return contracts.StatusMemoryLimitExceeded, nil
	default:
		return "", fmt.Errorf("%w: %q", execution.ErrUnsupportedFakeStatus, value)
	}
}

func resultForStatus(status contracts.ExecutionStatus, command execution.ExecutionCommand) execution.ExecutionResult {
	switch status {
	case contracts.StatusFailed:
		exitCode := 1
		return execution.ExecutionResult{
			Status:          contracts.StatusFailed,
			TestsTotal:      1,
			TestsFailed:     1,
			FirstFailedTest: "fake::test_failure",
			TestCases: []contracts.TestCaseResult{
				{Name: "fake::test_failure", Status: "failed", DurationMS: 1, Message: "fake executor forced failure"},
			},
			Stdout:          "fake executor: failed\n",
			Stderr:          "fake test failure\n",
			CPUms:           1,
			MemoryPeakBytes: 8 * 1024 * 1024,
			ExitCode:        &exitCode,
			RunMS:           1,
		}
	case contracts.StatusTimeLimitExceeded:
		return execution.ExecutionResult{
			Status:          contracts.StatusTimeLimitExceeded,
			Stdout:          "fake executor: time limit exceeded\n",
			CPUms:           command.Limits.CPUms,
			MemoryPeakBytes: 8 * 1024 * 1024,
			RunMS:           command.Limits.Wallms,
			Error: &contracts.RunError{
				Code:      "tle",
				Message:   "fake executor forced time limit exceeded",
				Retryable: false,
			},
		}
	case contracts.StatusMemoryLimitExceeded:
		return execution.ExecutionResult{
			Status:          contracts.StatusMemoryLimitExceeded,
			Stdout:          "fake executor: memory limit exceeded\n",
			CPUms:           1,
			MemoryPeakBytes: int64(command.Limits.MemoryMB) * 1024 * 1024,
			RunMS:           1,
			Error: &contracts.RunError{
				Code:      "mle",
				Message:   "fake executor forced memory limit exceeded",
				Retryable: false,
			},
		}
	default:
		exitCode := 0
		return execution.ExecutionResult{
			Status:      contracts.StatusPassed,
			TestsTotal:  1,
			TestsPassed: 1,
			TestCases: []contracts.TestCaseResult{
				{Name: "fake::test_success", Status: "passed", DurationMS: 1},
			},
			Stdout:          "fake executor: passed\n",
			CPUms:           1,
			MemoryPeakBytes: 8 * 1024 * 1024,
			ExitCode:        &exitCode,
			RunMS:           1,
		}
	}
}
