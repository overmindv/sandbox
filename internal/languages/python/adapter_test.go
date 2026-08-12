package python

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

func TestBuildCommandGeneratesPytestWorkspace(t *testing.T) {
	registry := testPythonRegistry(t)
	if registry == nil {
		return
	}

	command, err := registry.BuildCommand(context.Background(), validPythonRunRequest())
	if err != nil {
		t.Errorf("build command: %v", err)
		return
	}

	wantArgv := []string{
		"python3",
		"-m",
		"pytest",
		"-q",
		"--disable-warnings",
		"--tb=short",
		"-p",
		"_sandbox_pytest_plugin",
		"test_solution.py",
	}
	if !reflect.DeepEqual(command.Argv, wantArgv) {
		t.Errorf("expected pytest argv %#v, got %#v", wantArgv, command.Argv)
	}
	if strings.Contains(strings.Join(command.Argv, "\x00"), "sh -c") {
		t.Errorf("pytest argv must not invoke a shell: %#v", command.Argv)
	}
	if command.Environment["SANDBOX_PYTEST_REPORT"] != ReportPath {
		t.Errorf("expected pytest report env, got %#v", command.Environment)
	}
	if !containsString(command.CopyOut, ReportPath) {
		t.Errorf("expected copy-out path %q, got %#v", ReportPath, command.CopyOut)
	}
	if !containsExecutionFile(command.Files, "solution.py", execution.FileKindSource) {
		t.Errorf("expected source file in workspace, got %#v", command.Files)
	}
	if !containsExecutionFile(command.Files, "test_solution.py", execution.FileKindTest) {
		t.Errorf("expected test file in workspace, got %#v", command.Files)
	}
	if !containsExecutionFile(command.Files, HarnessPluginPath, execution.FileKindHarness) {
		t.Errorf("expected harness file in workspace, got %#v", command.Files)
	}
	if command.RuntimeLimits.MemoryBytes != 128*1024*1024 {
		t.Errorf("expected memory limit in bytes, got %d", command.RuntimeLimits.MemoryBytes)
	}
	if command.RuntimeLimits.WorkspaceBytes != 1048576 {
		t.Errorf("expected workspace limit in bytes, got %d", command.RuntimeLimits.WorkspaceBytes)
	}
}

func TestBuildCommandRejectsReservedWorkspacePaths(t *testing.T) {
	tests := []struct {
		name       string
		modifyFunc func(*contracts.RunRequest)
	}{
		{
			name: "harness plugin path",
			modifyFunc: func(req *contracts.RunRequest) {
				req.Code.Files[0].Path = HarnessPluginPath
			},
		},
		{
			name: "sandbox report path",
			modifyFunc: func(req *contracts.RunRequest) {
				req.Tests.Files[0].Path = ReportPath
			},
		},
		{
			name: "sandbox directory",
			modifyFunc: func(req *contracts.RunRequest) {
				req.Tests.Files[0].Path = ".sandbox/custom.json"
			},
		},
	}

	registry := testPythonRegistry(t)
	if registry == nil {
		return
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPythonRunRequest()
			tt.modifyFunc(&req)

			_, err := registry.BuildCommand(context.Background(), req)
			if !errors.Is(err, ErrReservedWorkspacePath) {
				t.Errorf("expected reserved path error, got %v", err)
			}
		})
	}
}

func TestBuildCommandRejectsTooManyFiles(t *testing.T) {
	registry := testPythonRegistry(t)
	if registry == nil {
		return
	}

	req := validPythonRunRequest()
	for i := 0; i < 32; i++ {
		req.Code.Files = append(req.Code.Files, contracts.SourceFile{
			Path:       "extra_" + string(rune('a'+i)) + ".py",
			ContentB64: encodePythonFixture("VALUE = 1\n"),
		})
	}

	_, err := registry.BuildCommand(context.Background(), req)
	if err == nil {
		t.Error("expected max file count error")
	}
}

func TestParsePytestResultPassed(t *testing.T) {
	result := ParsePytestResult(PytestResultInput{Raw: execution.RawExecutionResult{
		ExitCode: 0,
		ReportFiles: reportFile(`{
			"schema_version":"sandbox.pytest.report.v1",
			"exitstatus":0,
			"tests":[{"name":"test_solution.py::test_add","status":"passed","duration_ms":2}]
		}`),
	}})

	if result.Status != contracts.StatusPassed {
		t.Errorf("expected passed, got %q", result.Status)
	}
	if result.TestsTotal != 1 || result.TestsPassed != 1 || result.TestsFailed != 0 {
		t.Errorf("unexpected summary: total=%d passed=%d failed=%d", result.TestsTotal, result.TestsPassed, result.TestsFailed)
	}
}

func TestParsePytestResultFailed(t *testing.T) {
	result := ParsePytestResult(PytestResultInput{Raw: execution.RawExecutionResult{
		ExitCode: 1,
		ReportFiles: reportFile(`{
			"schema_version":"sandbox.pytest.report.v1",
			"exitstatus":1,
			"tests":[
				{"name":"test_solution.py::test_add","status":"passed","duration_ms":1},
				{"name":"test_solution.py::test_subtract","status":"failed","duration_ms":2,"message":"assert 1 == 2"}
			]
		}`),
	}})

	if result.Status != contracts.StatusFailed {
		t.Errorf("expected failed, got %q", result.Status)
	}
	if result.TestsTotal != 2 || result.TestsPassed != 1 || result.TestsFailed != 1 {
		t.Errorf("unexpected summary: total=%d passed=%d failed=%d", result.TestsTotal, result.TestsPassed, result.TestsFailed)
	}
	if result.FirstFailedTest != "test_solution.py::test_subtract" {
		t.Errorf("expected first failed test, got %q", result.FirstFailedTest)
	}
}

func TestParsePytestResultSyntaxError(t *testing.T) {
	result := ParsePytestResult(PytestResultInput{Raw: execution.RawExecutionResult{
		ExitCode: 2,
		Stderr:   "SyntaxError: invalid syntax",
		ReportFiles: reportFile(`{
			"schema_version":"sandbox.pytest.report.v1",
			"exitstatus":2,
			"tests":[{"name":"collection","status":"error","message":"SyntaxError: invalid syntax"}]
		}`),
	}})

	if result.Status != contracts.StatusCompileError {
		t.Errorf("expected compile_error, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Code != "syntax_error" {
		t.Errorf("expected syntax_error run error, got %#v", result.Error)
	}
}

func TestParsePytestResultTimeout(t *testing.T) {
	result := ParsePytestResult(PytestResultInput{Raw: execution.RawExecutionResult{
		TimedOut: true,
		RunMS:    5000,
	}})

	if result.Status != contracts.StatusTimeLimitExceeded {
		t.Errorf("expected tle, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Code != "tle" {
		t.Errorf("expected tle run error, got %#v", result.Error)
	}
}

func TestParsePytestResultMemoryExceeded(t *testing.T) {
	result := ParsePytestResult(PytestResultInput{Raw: execution.RawExecutionResult{
		MemoryExceeded:  true,
		MemoryPeakBytes: 128 * 1024 * 1024,
	}})

	if result.Status != contracts.StatusMemoryLimitExceeded {
		t.Errorf("expected mle, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Code != "mle" {
		t.Errorf("expected mle run error, got %#v", result.Error)
	}
}

func TestParsePytestResultOutputOverflow(t *testing.T) {
	result := ParsePytestResult(PytestResultInput{Raw: execution.RawExecutionResult{
		OutputExceeded: true,
		Stdout:         strings.Repeat("x", 32),
	}})

	if result.Status != contracts.StatusOutputLimitExceeded {
		t.Errorf("expected ole, got %q", result.Status)
	}
	if !result.LogsTruncated {
		t.Error("expected truncated logs marker")
	}
}

func testPythonRegistry(t *testing.T) *languages.Registry {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Errorf("load manifest: %v", err)
		return nil
	}
	registry, err := languages.NewRegistry(manifest, NewAdapter())
	if err != nil {
		t.Errorf("new registry: %v", err)
		return nil
	}
	return registry
}

func validPythonRunRequest() contracts.RunRequest {
	return contracts.RunRequest{
		SchemaVersion: contracts.RunRequestSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		UserID:        "33333333-3333-4333-8333-333333333333",
		TaskID:        "44444444-4444-4444-8444-444444444444",
		Language: contracts.LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Code: contracts.CodeSpec{
			Files: []contracts.SourceFile{
				{
					Path:       "solution.py",
					ContentB64: encodePythonFixture("def add(a, b):\n    return a + b\n"),
				},
			},
		},
		Tests: contracts.TestSpec{
			Mode:   contracts.TestModePytest,
			Source: contracts.TestSourceInline,
			Files: []contracts.SourceFile{
				{
					Path:       "test_solution.py",
					ContentB64: encodePythonFixture("from solution import add\n\n\ndef test_add():\n    assert add(1, 2) == 3\n"),
				},
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
		TraceID:   "trace-python",
		CreatedAt: time.Now().UTC(),
	}
}

func encodePythonFixture(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func reportFile(value string) map[string][]byte {
	return map[string][]byte{ReportPath: []byte(value)}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsExecutionFile(files []execution.ExecutionFile, path string, kind execution.FileKind) bool {
	for _, file := range files {
		if file.Path == path && file.Kind == kind {
			return true
		}
	}
	return false
}
