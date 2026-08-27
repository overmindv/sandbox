package fake

import (
	"context"
	"errors"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

func TestFakeExecutorReturnsStatusFromMetadata(t *testing.T) {
	tests := []struct {
		name       string
		fakeValue  string
		wantStatus contracts.ExecutionStatus
	}{
		{name: "success", fakeValue: "success", wantStatus: contracts.StatusPassed},
		{name: "fail", fakeValue: "fail", wantStatus: contracts.StatusFailed},
		{name: "tle", fakeValue: "tle", wantStatus: contracts.StatusTimeLimitExceeded},
		{name: "mle", fakeValue: "mle", wantStatus: contracts.StatusMemoryLimitExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := NewExecutor(config.FakeExecutor{DefaultStatus: "passed"})
			if err != nil {
				t.Errorf("new executor: %v", err)
			}

			result, err := executor.Execute(context.Background(), validExecutionCommand(map[string]any{"fake_status": tt.fakeValue}))
			if err != nil {
				t.Errorf("execute fake command: %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, result.Status)
			}
		})
	}
}

func TestFakeExecutorReturnsStatusFromFileMarker(t *testing.T) {
	executor, err := NewExecutor(config.FakeExecutor{DefaultStatus: "passed"})
	if err != nil {
		t.Errorf("new executor: %v", err)
	}

	command := validExecutionCommand(nil)
	command.Files = append(command.Files, execution.ExecutionFile{
		Path:    "test_solution.py",
		Kind:    execution.FileKindTest,
		Content: []byte("# fake_status=mle\n"),
	})

	result, err := executor.Execute(context.Background(), command)
	if err != nil {
		t.Errorf("execute fake command: %v", err)
	}
	if result.Status != contracts.StatusMemoryLimitExceeded {
		t.Errorf("expected mle, got %q", result.Status)
	}
}

func TestFakeExecutorRejectsUnknownStatus(t *testing.T) {
	executor, err := NewExecutor(config.FakeExecutor{DefaultStatus: "passed"})
	if err != nil {
		t.Errorf("new executor: %v", err)
	}

	_, err = executor.Execute(context.Background(), validExecutionCommand(map[string]any{"fake_status": "explode"}))
	if !errors.Is(err, execution.ErrUnsupportedFakeStatus) {
		t.Errorf("expected unsupported fake status, got %v", err)
	}
}

func validExecutionCommand(metadata map[string]any) execution.ExecutionCommand {
	if metadata == nil {
		metadata = map[string]any{}
	}
	return execution.ExecutionCommand{
		ID:        "attempt-1",
		AttemptID: "attempt-1",
		Language: contracts.LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Files: []execution.ExecutionFile{
			{
				Path:    "solution.py",
				Kind:    execution.FileKindSource,
				Content: []byte("def add(a, b): return a + b\n"),
			},
		},
		Limits: contracts.Limits{
			CPUms:          2000,
			Wallms:         5000,
			MemoryMB:       128,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
		Metadata: metadata,
	}
}
