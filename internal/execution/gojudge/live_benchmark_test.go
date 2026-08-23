package gojudge

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages/python"
)

func BenchmarkLiveGoJudgePythonExecution(b *testing.B) {
	if os.Getenv("SANDBOX_GOJUDGE_BENCH") != "1" {
		b.Skip("set SANDBOX_GOJUDGE_BENCH=1 to run live go-judge benchmarks")
	}

	cfg := benchmarkGoJudgeConfig()
	b.Run("cold_client", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			executor, err := NewExecutor(cfg)
			if err != nil {
				b.Errorf("new go-judge executor: %v", err)
				return
			}
			command := benchmarkLivePythonCommand(uint64(i))
			if err := benchmarkExecutePython(b, executor, command); err != nil {
				b.Errorf("execute cold client command: %v", err)
				return
			}
		}
	})

	executor, err := NewExecutor(cfg)
	if err != nil {
		b.Errorf("new go-judge executor: %v", err)
		return
	}

	b.Run("warm_serial", func(b *testing.B) {
		if err := benchmarkExecutePython(b, executor, benchmarkLivePythonCommand(0)); err != nil {
			b.Errorf("warm up go-judge executor: %v", err)
			return
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			command := benchmarkLivePythonCommand(uint64(i + 1))
			if err := benchmarkExecutePython(b, executor, command); err != nil {
				b.Errorf("execute warm command: %v", err)
				return
			}
		}
	})

	b.Run("warm_parallel", func(b *testing.B) {
		if err := benchmarkExecutePython(b, executor, benchmarkLivePythonCommand(0)); err != nil {
			b.Errorf("warm up go-judge executor: %v", err)
			return
		}

		var sequence uint64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				id := atomic.AddUint64(&sequence, 1)
				command := benchmarkLivePythonCommand(id)
				if err := benchmarkExecutePython(b, executor, command); err != nil {
					b.Errorf("execute parallel command: %v", err)
					return
				}
			}
		})
	})
}

func benchmarkGoJudgeConfig() config.GoJudgeClient {
	baseURL := os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:5050"
	}
	return config.GoJudgeClient{
		BaseURL:            baseURL,
		RequestTimeoutMS:   30000,
		MaxIdleConnections: 64,
	}
}

func benchmarkExecutePython(b *testing.B, executor *Executor, command execution.ExecutionCommand) error {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := executor.Execute(ctx, command)
	if err != nil {
		return err
	}
	if result.Status != contracts.StatusPassed {
		return fmt.Errorf("expected passed result, got %#v", result)
	}
	return nil
}

func benchmarkLivePythonCommand(sequence uint64) execution.ExecutionCommand {
	command := validGoJudgeCommand()
	attemptID := fmt.Sprintf("benchmark-live-gojudge-%d", sequence)
	command.ID = attemptID
	command.AttemptID = attemptID
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
	return command
}
