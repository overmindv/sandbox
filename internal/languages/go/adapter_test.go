package golang

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/python"
)

func TestBuildCommandGeneratesGoTestWorkspace(t *testing.T) {
	registry := testGoRegistry(t)
	if registry == nil {
		return
	}

	command, err := registry.BuildCommand(context.Background(), validGoRunRequest())
	if err != nil {
		t.Errorf("build command: %v", err)
		return
	}

	wantArgv := []string{"/usr/local/go/bin/go", "test", "-json", "-p", "1", "./..."}
	if !reflect.DeepEqual(command.Argv, wantArgv) {
		t.Errorf("expected Go argv %#v, got %#v", wantArgv, command.Argv)
	}
	if strings.Contains(strings.Join(command.Argv, "\x00"), "sh -c") {
		t.Errorf("Go argv must not invoke a shell: %#v", command.Argv)
	}
	if command.Adapter != AdapterName {
		t.Errorf("expected adapter %q, got %q", AdapterName, command.Adapter)
	}
	if command.Environment["GOPROXY"] != "off" {
		t.Errorf("expected Go environment from manifest, got %#v", command.Environment)
	}
	if command.Environment["GOMAXPROCS"] != "2" {
		t.Errorf("expected Go GOMAXPROCS from manifest, got %#v", command.Environment)
	}
	if command.Environment["PATH"] != "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin" {
		t.Errorf("expected Go PATH from manifest, got %#v", command.Environment)
	}
	if !containsGoExecutionFile(command.Files, "solution.go", execution.FileKindSource) {
		t.Errorf("expected source file in workspace, got %#v", command.Files)
	}
	if !containsGoExecutionFile(command.Files, "solution_test.go", execution.FileKindTest) {
		t.Errorf("expected test file in workspace, got %#v", command.Files)
	}
	if !containsGoExecutionFile(command.Files, "go.mod", execution.FileKindHarness) {
		t.Errorf("expected generated go.mod in workspace, got %#v", command.Files)
	}
	if command.RuntimeLimits.MemoryBytes != 128*1024*1024 {
		t.Errorf("expected memory limit in bytes, got %d", command.RuntimeLimits.MemoryBytes)
	}
}

func TestBuildCommandKeepsUserGoMod(t *testing.T) {
	registry := testGoRegistry(t)
	if registry == nil {
		return
	}

	req := validGoRunRequest()
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{
		Path:       "go.mod",
		ContentB64: encodeGoFixture("module custom\n\ngo 1.26\n"),
	})

	command, err := registry.BuildCommand(context.Background(), req)
	if err != nil {
		t.Errorf("build command: %v", err)
		return
	}

	var goModCount int
	for _, file := range command.Files {
		if strings.EqualFold(file.Path, "go.mod") {
			goModCount++
			if file.Kind != execution.FileKindSource {
				t.Errorf("expected user go.mod to stay source kind, got %q", file.Kind)
			}
		}
	}
	if goModCount != 1 {
		t.Errorf("expected exactly one go.mod, got %d in %#v", goModCount, command.Files)
	}
}

func TestBuildCommandRejectsDisallowedGoExtension(t *testing.T) {
	registry := testGoRegistry(t)
	if registry == nil {
		return
	}

	req := validGoRunRequest()
	req.Code.Files[0].Path = "solution.py"

	_, err := registry.BuildCommand(context.Background(), req)
	if err == nil {
		t.Error("expected disallowed extension error")
	}
}

func TestParseGoTestResultPassed(t *testing.T) {
	result := ParseGoTestResult(GoTestResultInput{Raw: execution.RawExecutionResult{
		ExitCode: 0,
		Stdout: strings.Join([]string{
			`{"Action":"run","Package":"sandbox","Test":"TestAdd"}`,
			`{"Action":"pass","Package":"sandbox","Test":"TestAdd","Elapsed":0.002}`,
			`{"Action":"pass","Package":"sandbox","Elapsed":0.003}`,
		}, "\n"),
	}})

	if result.Status != contracts.StatusPassed {
		t.Errorf("expected passed, got %q", result.Status)
	}
	if result.TestsTotal != 1 || result.TestsPassed != 1 || result.TestsFailed != 0 {
		t.Errorf("unexpected summary: total=%d passed=%d failed=%d", result.TestsTotal, result.TestsPassed, result.TestsFailed)
	}
}

func TestParseGoTestResultFailed(t *testing.T) {
	result := ParseGoTestResult(GoTestResultInput{Raw: execution.RawExecutionResult{
		ExitCode: 1,
		Stdout: strings.Join([]string{
			`{"Action":"run","Package":"sandbox","Test":"TestAdd"}`,
			`{"Action":"fail","Package":"sandbox","Test":"TestAdd","Elapsed":0.004}`,
			`{"Action":"fail","Package":"sandbox","Elapsed":0.005}`,
		}, "\n"),
	}})

	if result.Status != contracts.StatusFailed {
		t.Errorf("expected failed, got %q", result.Status)
	}
	if result.TestsTotal != 1 || result.TestsPassed != 0 || result.TestsFailed != 1 {
		t.Errorf("unexpected summary: total=%d passed=%d failed=%d", result.TestsTotal, result.TestsPassed, result.TestsFailed)
	}
	if result.FirstFailedTest != "sandbox::TestAdd" {
		t.Errorf("expected first failed test, got %q", result.FirstFailedTest)
	}
}

func TestParseGoTestResultCompileError(t *testing.T) {
	result := ParseGoTestResult(GoTestResultInput{Raw: execution.RawExecutionResult{
		ExitCode: 1,
		Stdout:   "# sandbox\n./solution.go:3:9: undefined: missing\nFAIL\tsandbox [build failed]\n",
	}})

	if result.Status != contracts.StatusCompileError {
		t.Errorf("expected compile_error, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Code != "go_compile_error" {
		t.Errorf("expected go_compile_error, got %#v", result.Error)
	}
}

func TestParseGoTestResultTimeout(t *testing.T) {
	result := ParseGoTestResult(GoTestResultInput{Raw: execution.RawExecutionResult{
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

func testGoRegistry(t *testing.T) *languages.Registry {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Errorf("load manifest: %v", err)
		return nil
	}
	registry, err := languages.NewRegistry(manifest, python.NewAdapter(), NewAdapter())
	if err != nil {
		t.Errorf("new registry: %v", err)
		return nil
	}
	return registry
}

func validGoRunRequest() contracts.RunRequest {
	return contracts.RunRequest{
		SchemaVersion: contracts.RunRequestSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		UserID:        "33333333-3333-4333-8333-333333333333",
		TaskID:        "44444444-4444-4444-8444-444444444444",
		Language: contracts.LanguageSpec{
			ID:      "go",
			Version: "1.26",
		},
		Code: contracts.CodeSpec{
			Entrypoint: "solution.go",
			Files: []contracts.SourceFile{
				{
					Path:       "solution.go",
					ContentB64: encodeGoFixture("package solution\n\nfunc Add(a, b int) int { return a + b }\n"),
				},
			},
		},
		Tests: contracts.TestSpec{
			Mode:       contracts.TestModeGoTest,
			Source:     contracts.TestSourceInline,
			Entrypoint: "solution_test.go",
			Files: []contracts.SourceFile{
				{
					Path:       "solution_test.go",
					ContentB64: encodeGoFixture("package solution\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(1, 2); got != 3 {\n\t\tt.Errorf(\"Add() = %d\", got)\n\t}\n}\n"),
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
		TraceID:   "trace-go",
		CreatedAt: time.Now().UTC(),
	}
}

func encodeGoFixture(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func containsGoExecutionFile(files []execution.ExecutionFile, path string, kind execution.FileKind) bool {
	for _, file := range files {
		if file.Path == path && file.Kind == kind {
			return true
		}
	}
	return false
}
