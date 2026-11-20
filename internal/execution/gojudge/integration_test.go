package gojudge

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/python"
)

func TestLiveGoJudgeRuntimeVersions(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" {
		t.Skip("set SANDBOX_GOJUDGE_INTEGRATION=1 to run live go-judge integration test")
	}
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(liveGoJudgeConfig())
	if err != nil {
		t.Fatal(err)
	}

	for _, language := range manifest.Languages {
		if !language.Enabled {
			continue
		}
		var argv []string
		var prefix string
		switch language.ID {
		case "python":
			argv = []string{"python3", "--version"}
			prefix = "Python "
		case "go":
			argv = []string{"/usr/local/go/bin/go", "env", "GOVERSION"}
			prefix = "go"
		default:
			continue
		}
		t.Run(language.ID, func(t *testing.T) {
			var want string
			for _, version := range language.Versions {
				if version.Default {
					want = version.RuntimeVersion
					break
				}
			}
			if want == "" {
				t.Fatal("runtime_version is missing from the manifest")
			}
			cmd := validGoJudgeCommand()
			cmd.Argv = argv
			cmd.Environment = map[string]string{"GOTOOLCHAIN": "local"}
			cmd.CopyOut = nil
			req, err := RequestFromExecutionCommand(cmd)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			results, err := client.Run(ctx, req)
			if err != nil || len(results) != 1 {
				t.Fatal("version probe failed", err)
			}
			got := strings.TrimSpace(results[0].Files[stdoutName])
			if results[0].Status != StatusAccepted || got != prefix+want {
				t.Errorf("want %q, got %q with status %q", prefix+want, got, results[0].Status)
			}
		})
	}
}

func TestLiveGoJudgeRunsPythonPytest(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" {
		t.Skip("set SANDBOX_GOJUDGE_INTEGRATION=1 to run live go-judge integration test")
	}

	executor, err := NewExecutor(liveGoJudgeConfig())
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

func TestLiveGoJudgeRunsGoTest(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" {
		t.Skip("set SANDBOX_GOJUDGE_INTEGRATION=1 to run live go-judge integration test")
	}

	limits, err := config.LoadLimits("configs/limits.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := limits.Profiles["go-default"]
	if !ok {
		t.Fatal("go-default limits are missing")
	}
	cfg := liveGoJudgeConfig()
	cfg.CompilerURL = os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	if cfg.CompilerURL == "" {
		t.Skip("set SANDBOX_GOJUDGE_COMPILER_URL for the cold Go build")
	}
	cfg.RequestTimeoutMS = profile.WallMS + 5000
	executor, err := NewExecutor(cfg)
	if err != nil {
		t.Errorf("new go-judge executor: %v", err)
		return
	}

	command := validGoJudgeCommand()
	command.ID = "live-go-test"
	command.AttemptID = "live-go-test"
	command.Language = contracts.LanguageSpec{ID: "go", Version: "1.26"}
	command.Adapter = "go"
	command.RuntimeLimits = execution.RuntimeLimits{
		CPUms: profile.CPUms, WallMS: profile.WallMS, MemoryBytes: int64(profile.MemoryMB) << 20,
		PIDs: profile.PIDs, StdoutBytes: profile.StdoutBytes, StderrBytes: profile.StderrBytes,
		WorkspaceBytes: int64(profile.WorkspaceBytes),
	}
	command.Argv = []string{"/usr/local/go/bin/go", "test", "-json", "-p", "1", "./..."}
	command.Environment = map[string]string{
		"CGO_ENABLED": "0",
		"GOCACHE":     "/tmp/go-build",
		"GOMODCACHE":  "/tmp/go-mod",
		"GOMAXPROCS":  "2",
		"GOTOOLCHAIN": "local",
		"GOPROXY":     "off",
		"PATH":        "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
	}
	command.CopyOut = nil
	command.Files = []execution.ExecutionFile{
		{
			Path:    "go.mod",
			Kind:    execution.FileKindHarness,
			Content: []byte("module sandbox\n\ngo 1.26\n"),
		},
		{
			Path:    "solution.go",
			Kind:    execution.FileKindSource,
			Content: []byte("package solution\n\nfunc Add(a, b int) int { return a + b }\n"),
		},
		{
			Path:    "solution_test.go",
			Kind:    execution.FileKindTest,
			Content: []byte("package solution\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(1, 2); got != 3 {\n\t\tt.Errorf(\"Add() = %d\", got)\n\t}\n}\n"),
		},
	}
	command.TestMode = contracts.TestModeGoTest

	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(profile.WallMS)*time.Millisecond+executor.BuildAllowance(command))
	defer cancel()
	result, err := executor.Execute(ctx, command)
	if err != nil {
		t.Errorf("execute live go-judge Go command: %v", err)
		return
	}
	if result.Status != contracts.StatusPassed {
		t.Errorf("expected passed result, got %#v", result)
	}
	if result.TestsPassed != 1 {
		t.Errorf("expected one passed Go test case, got %#v", result)
	}
}

func liveGoJudgeConfig() config.GoJudgeClient {
	baseURL := os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:5050"
	}
	return config.GoJudgeClient{
		BaseURL:            baseURL,
		RequestTimeoutMS:   10000,
		MaxIdleConnections: 2,
	}
}
