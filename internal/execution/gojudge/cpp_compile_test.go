package gojudge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/cpp"
)

const cppSource = `#include <iostream>
int main() {
    int n;
    if (std::cin >> n) std::cout << n * 2 << '\n';
}`

func TestCPPCompilerAndRunnerReceiveOnlyTheirInputs(t *testing.T) {
	t.Parallel()

	command := cppCommand(t, cppSource)
	compiler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			var request Request
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Cmd) != 1 {
				t.Error("invalid compile request", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			cmd := request.Cmd[0]
			if !slices.Equal(cmd.Args, command.CompileArgv) || len(cmd.CopyIn) != 1 || cmd.CopyIn["solution.cpp"].Content == nil ||
				cmd.Files[0].Content == nil || *cmd.Files[0].Content != "" || cmd.ClockLimit != int64(cppCompileWall) ||
				cmd.CPULimit != int64(8*time.Second) || cmd.MemoryLimit != 512<<20 ||
				!slices.Equal(cmd.CopyOutCached, []string{cpp.ArtifactName + "?"}) {
				t.Error("compiler received unexpected inputs or limits")
			}
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":""},"fileIds":{"_sandbox_cpp":"ABCDEFGH"}}]`)
		case "GET /file/ABCDEFGH":
			_, _ = io.WriteString(w, "\x7fELFfixture")
		case "DELETE /file/ABCDEFGH":
		default:
			http.NotFound(w, r)
		}
	}))
	defer compiler.Close()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /file":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, `"JKLMNOPQ"`)
		case "POST /run":
			checkCPPCases(t, r, command)
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":"2"}},{"status":"Accepted","exitStatus":0,"files":{"stdout":"4"}}]`)
		case "DELETE /file/JKLMNOPQ":
		default:
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), command)
	if err != nil || result.Status != contracts.StatusOK || len(result.Cases) != 2 {
		t.Fatal("unexpected result", result.Status, err)
	}
}

func checkCPPCases(t *testing.T, r *http.Request, command execution.ExecutionCommand) {
	t.Helper()

	var request Request
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Cmd) != len(command.Inputs) {
		t.Error("invalid execution request", err)
		return
	}
	for i, cmd := range request.Cmd {
		if !slices.Equal(cmd.Args, command.Argv) || len(cmd.CopyIn) != 1 || cmd.CopyIn[cpp.ArtifactName].FileID != "JKLMNOPQ" ||
			cmd.CopyIn[cpp.ArtifactName].Content != nil || cmd.Files[0].Content == nil || *cmd.Files[0].Content != command.Inputs[i] ||
			cmd.CPULimit != millisToNanos(command.RuntimeLimits.CPUms) || cmd.ClockLimit != millisToNanos(command.RuntimeLimits.WallMS) ||
			cmd.ProcLimit != command.RuntimeLimits.PIDs || cmd.MemoryLimit != command.RuntimeLimits.MemoryBytes ||
			cmd.Files[1].Max != command.RuntimeLimits.StdoutBytes || cmd.Files[2].Max != command.RuntimeLimits.StderrBytes {
			t.Error("case received unexpected files, input or limits", i)
		}
	}
}

func TestCPPRequiresSeparateCompiler(t *testing.T) {
	t.Parallel()

	runner := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("C++ must not fall back to running on the runtime endpoint")
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), cppCommand(t, cppSource)); !errors.Is(err, ErrInfrastructure) {
		t.Error("expected a missing compiler error", err)
	}
}

func TestLiveGoJudgeCPP(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_CPP_INTEGRATION") != "1" {
		t.Skip("requires an isolated C++ compiler and runtime")
	}
	cfg := liveGoJudgeConfig()
	cfg.CompilerURL = os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	if cfg.CompilerURL == "" {
		t.Fatal("SANDBOX_GOJUDGE_COMPILER_URL is required")
	}
	executor, err := NewExecutor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("compile time", func(t *testing.T) {
		command := cppCommand(t, cppSource)
		run, err := RequestFromExecutionCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		ctx, stop := context.WithTimeout(t.Context(), cppCompileBudget)
		defer stop()
		start := time.Now()
		binary, verdict, err := executor.buildCPP(ctx, command.AttemptID, command.CompileArgv, run.Cmd[0].CopyIn)
		if err != nil || verdict != nil || !isELF(binary) {
			t.Fatal("compiler did not return an executable", verdict, err)
		}
		t.Log("compiler round trip", time.Since(start), "budget", cppCompileBudget, "artifact bytes", len(binary))
	})
	for _, tc := range []struct {
		name   string
		source string
		status contracts.ExecutionStatus
		limit  func(*execution.ExecutionCommand)
		output []string
	}{
		{name: "accepted", source: cppSource, status: contracts.StatusOK, output: []string{"2\n", "4\n"}},
		{name: "wrong output", source: `#include <cstdio>
int main() { std::puts("0"); }`, status: contracts.StatusOK, output: []string{"0\n", "0\n"}},
		{name: "syntax error", source: "int main( {", status: contracts.StatusCompileError},
		{name: "wall limit", source: `#include <thread>
int main() { std::this_thread::sleep_for(std::chrono::milliseconds(300)); }`,
			status: contracts.StatusTimeLimitExceeded, limit: func(c *execution.ExecutionCommand) { c.RuntimeLimits.WallMS = 80 }},
		{name: "output limit", source: `#include <cstdio>
int main() { std::puts("01234567890123456789"); }`,
			status: contracts.StatusOutputLimitExceeded, limit: func(c *execution.ExecutionCommand) { c.RuntimeLimits.StdoutBytes = 8 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := cppCommand(t, tc.source)
			if tc.limit != nil {
				tc.limit(&command)
			}
			ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
			defer stop()
			start := time.Now()
			result, err := executor.Execute(ctx, command)
			if err != nil || result.Status != tc.status {
				t.Fatal("unexpected C++ outcome", result.Status, result.Error, err)
			}
			caseWall := make([]int, 0, len(result.Cases))
			for _, caseResult := range result.Cases {
				caseWall = append(caseWall, caseResult.DurationMS)
			}
			t.Log("compile and run", time.Since(start), "case wall ms", caseWall, "case limit ms", command.RuntimeLimits.WallMS)
			if tc.output != nil {
				if len(result.Cases) != len(tc.output) {
					t.Fatal("wrong number of results", len(result.Cases))
				}
				for i, want := range tc.output {
					if result.Cases[i].Stdout != want {
						t.Error("unexpected stdout", i, result.Cases[i].Stdout)
					}
				}
			}
		})
	}
}

func cppCommand(t *testing.T, source string) execution.ExecutionCommand {
	t.Helper()

	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID != cpp.AdapterName {
			continue
		}
		req := contracts.RunRequest{
			AttemptID: "cpp-fixture",
			Language:  contracts.LanguageSpec{ID: cpp.AdapterName, Version: "17"},
			Code: contracts.CodeSpec{Files: []contracts.SourceFile{{
				Path: "solution.cpp", ContentB64: base64.StdEncoding.EncodeToString([]byte(source)),
			}}},
			Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
			Limits: contracts.Limits{CPUms: 60, Wallms: 500, MemoryMB: 64, PIDs: 8,
				StdoutBytes: 1024, StderrBytes: 1024, WorkspaceBytes: 5 << 20},
		}
		cmd, err := cpp.NewAdapter().BuildCommand(t.Context(), req, languages.ResolvedLanguage{Version: language.Versions[0]})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	t.Fatal("C++ is missing from the manifest")
	return execution.ExecutionCommand{}
}
