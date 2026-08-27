package python

import (
	"context"
	"strings"
	"testing"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	golang "overmindv/sandbox/internal/languages/go"
)

func BenchmarkBuildCommandPythonPytest(b *testing.B) {
	registry := benchmarkPythonRegistry(b)
	if registry == nil {
		return
	}
	req := validPythonRunRequest()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := registry.BuildCommand(context.Background(), req); err != nil {
			b.Errorf("build command: %v", err)
			return
		}
	}
}

func BenchmarkBuildCommandPythonPytestManyFiles(b *testing.B) {
	registry := benchmarkPythonRegistry(b)
	if registry == nil {
		return
	}
	req := validPythonRunRequest()
	for i := 0; i < 10; i++ {
		req.Code.Files = append(req.Code.Files, contracts.SourceFile{
			Path:       "module_" + string(rune('a'+i)) + ".py",
			ContentB64: encodePythonFixture("VALUE = 1\n"),
		})
		req.Tests.Files = append(req.Tests.Files, contracts.SourceFile{
			Path:       "test_extra_" + string(rune('a'+i)) + ".py",
			ContentB64: encodePythonFixture("def test_extra():\n    assert True\n"),
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := registry.BuildCommand(context.Background(), req); err != nil {
			b.Errorf("build command with many files: %v", err)
			return
		}
	}
}

func BenchmarkBuildCommandPythonPytestParallel(b *testing.B) {
	registry := benchmarkPythonRegistry(b)
	if registry == nil {
		return
	}
	req := validPythonRunRequest()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := registry.BuildCommand(ctx, req); err != nil {
				b.Errorf("build command in parallel: %v", err)
				return
			}
		}
	})
}

func BenchmarkParsePytestReport(b *testing.B) {
	raw := execution.RawExecutionResult{
		ExitCode:    0,
		Stdout:      strings.Repeat(".", 100) + "\n100 passed in 0.42s\n",
		ReportFiles: reportFile(benchmarkPytestReport(100)),
		CPUms:       420,
		RunMS:       450,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := ParsePytestResult(PytestResultInput{Raw: raw})
		if result.Status != contracts.StatusPassed {
			b.Errorf("expected passed, got %q", result.Status)
			return
		}
	}
}

func benchmarkPythonRegistry(b *testing.B) *languages.Registry {
	b.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		b.Errorf("load manifest: %v", err)
		return nil
	}
	registry, err := languages.NewRegistry(manifest, NewAdapter(), golang.NewAdapter())
	if err != nil {
		b.Errorf("new registry: %v", err)
		return nil
	}
	return registry
}

func benchmarkPytestReport(count int) string {
	var builder strings.Builder
	builder.WriteString(`{"schema_version":"sandbox.pytest.report.v1","exitstatus":0,"tests":[`)
	for i := 0; i < count; i++ {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`{"name":"test_solution.py::test_case_`)
		builder.WriteString(string(rune('a' + (i % 26))))
		builder.WriteString(`","status":"passed","duration_ms":1}`)
	}
	builder.WriteString(`]}`)
	return builder.String()
}
