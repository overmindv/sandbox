package gojudge

import (
	"context"
	"os"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages/python"
)

func TestLiveGoJudgeRunsPythonPytest(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" {
		t.Skip("set SANDBOX_GOJUDGE_INTEGRATION=1 to run live go-judge integration test")
	}

	baseURL := os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:5050"
	}
	executor, err := NewExecutor(config.GoJudgeClient{
		BaseURL:            baseURL,
		RequestTimeoutMS:   10000,
		MaxIdleConnections: 2,
	})
	if err != nil {
		t.Errorf("new go-judge executor: %v", err)
		return
	}

	command := validGoJudgeCommand()
	command.Files = []execution.ExecutionFile{
		{
			Path:    "solution.py",
			Kind:    execution.FileKindSource,
			Content: []byte("def add(a, b):\n    return a + b\n"),
		},
		{
			Path:    "test_solution.py",
			Kind:    execution.FileKindTest,
			Content: []byte("from solution import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n"),
		},
		{
			Path:    python.HarnessPluginPath,
			Kind:    execution.FileKindHarness,
			Content: []byte(python.HarnessSource()),
		},
	}
	command.CopyOut = []string{python.ReportPath}
	command.Environment["SANDBOX_PYTEST_REPORT"] = python.ReportPath
	command.TestMode = contracts.TestModePytest

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := executor.Execute(ctx, command)
	if err != nil {
		t.Errorf("execute live go-judge command: %v", err)
		return
	}
	if result.Status != contracts.StatusPassed {
		t.Errorf("expected passed result, got %#v", result)
	}
	if result.TestsPassed != 1 {
		t.Errorf("expected one passed pytest case, got %#v", result)
	}
}
