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
	"overmindv/sandbox/internal/languages/native"
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
				cmd.Files[0].Content == nil || *cmd.Files[0].Content != "" || cmd.ClockLimit != int64(nativeCompileWall) ||
				cmd.CPULimit != int64(8*time.Second) || cmd.MemoryLimit != 512<<20 ||
				!slices.Equal(cmd.CopyOutCached, []string{"_sandbox_cpp?"}) {
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
		artifact := cmd.CopyIn["_sandbox_cpp"]
		if !slices.Equal(cmd.Args, command.Argv) || len(cmd.CopyIn) != 1 || artifact.FileID != "JKLMNOPQ" ||
			artifact.Content != nil || cmd.Files[0].Content == nil || *cmd.Files[0].Content != command.Inputs[i] ||
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
	commands := []execution.ExecutionCommand{
		cppCommand(t, cppSource),
		nativeCommand(t, native.C17(), nativeSource("solution.c", "int main(void) { return 0; }")),
	}
	for _, command := range commands {
		if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrInfrastructure) {
			t.Error("expected a missing compiler error", command.Adapter, err)
		}
	}
}

type nativeScenario struct {
	name   string
	files  []contracts.SourceFile
	status contracts.ExecutionStatus
	limit  func(*execution.ExecutionCommand)
	output []string
}

func TestLiveGoJudgeCPP(t *testing.T) {
	runLiveNative(t, native.CPP17(), []nativeScenario{
		{name: "accepted", files: cppFiles(cppSource), status: contracts.StatusOK, output: []string{"2\n", "4\n"}},
		{name: "wrong output", files: cppFiles(`#include <cstdio>
int main() { std::puts("0"); }`), status: contracts.StatusOK, output: []string{"0\n", "0\n"}},
		{name: "syntax error", files: cppFiles("int main( {"), status: contracts.StatusCompileError},
		{name: "wall limit", files: cppFiles(`#include <thread>
int main() { std::this_thread::sleep_for(std::chrono::milliseconds(300)); }`),
			status: contracts.StatusTimeLimitExceeded, limit: func(c *execution.ExecutionCommand) { c.RuntimeLimits.WallMS = 80 }},
		{name: "output limit", files: cppFiles(`#include <cstdio>
int main() { std::puts("01234567890123456789"); }`),
			status: contracts.StatusOutputLimitExceeded, limit: func(c *execution.ExecutionCommand) { c.RuntimeLimits.StdoutBytes = 8 }},
	})
}

func TestLiveGoJudgeC17(t *testing.T) {
	multiFile := []contracts.SourceFile{
		nativeSource("solution.c", `#if __STDC_VERSION__ != 201710L
#error expected C17
#endif
#include <stdio.h>
#include "calc.h"
int main(void) {
    int n;
    if (scanf("%d", &n) == 1) printf("%d\n", score(n));
}`),
		nativeSource("calc.h", "int score(int n);"),
		nativeSource("calc.c", `#include <math.h>
#include "calc.h"
int score(int n) { return n + (int)sqrt((double)n); }`),
	}
	cFiles := func(source string) []contracts.SourceFile {
		return []contracts.SourceFile{nativeSource("solution.c", source)}
	}
	runLiveNative(t, native.C17(), []nativeScenario{
		{name: "accepted multi-file", files: multiFile, status: contracts.StatusOK, output: []string{"2\n", "3\n"}},
		{name: "syntax error", files: cFiles("int main( {"), status: contracts.StatusCompileError},
		{name: "wall limit", files: cFiles(`#define _POSIX_C_SOURCE 200809L
#include <time.h>
int main(void) {
    struct timespec delay = {0, 300000000};
    nanosleep(&delay, NULL);
}`), status: contracts.StatusTimeLimitExceeded, limit: func(c *execution.ExecutionCommand) { c.RuntimeLimits.WallMS = 80 }},
		{name: "output limit", files: cFiles(`#include <stdio.h>
int main(void) { puts("01234567890123456789"); }`),
			status: contracts.StatusOutputLimitExceeded, limit: func(c *execution.ExecutionCommand) { c.RuntimeLimits.StdoutBytes = 8 }},
	})
}

func runLiveNative(t *testing.T, adapter native.Adapter, tests []nativeScenario) {
	t.Helper()

	if os.Getenv("SANDBOX_GOJUDGE_CPP_INTEGRATION") != "1" {
		t.Skip("requires an isolated compiler and runtime")
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
		command := nativeCommand(t, adapter, tests[0].files...)
		run, err := RequestFromExecutionCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		ctx, stop := context.WithTimeout(t.Context(), nativeCompileBudget)
		defer stop()
		start := time.Now()
		binary, verdict, err := executor.buildNative(ctx, adapter, command.AttemptID, command.CompileArgv, run.Cmd[0].CopyIn)
		if err != nil || verdict != nil || !isELF(binary) {
			t.Fatal("compiler did not return an executable", verdict, err)
		}
		t.Log("compiler round trip", time.Since(start), "budget", nativeCompileBudget, "artifact bytes", len(binary))
	})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			command := nativeCommand(t, adapter, tc.files...)
			if tc.limit != nil {
				tc.limit(&command)
			}
			ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
			defer stop()
			start := time.Now()
			result, err := executor.Execute(ctx, command)
			if err != nil || result.Status != tc.status {
				t.Fatal("unexpected result", result.Status, result.Error, err)
			}
			if tc.status == contracts.StatusCompileError && (result.Error == nil || result.Error.Code != adapter.Name()+"_compile_error") {
				t.Errorf("unexpected compile error code: %v", result.Error)
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

	return nativeCommand(t, native.CPP17(), cppFiles(source)...)
}

func cppFiles(source string) []contracts.SourceFile {
	return []contracts.SourceFile{nativeSource("solution.cpp", source)}
}

func nativeSource(name, content string) contracts.SourceFile {
	return contracts.SourceFile{Path: name, ContentB64: base64.StdEncoding.EncodeToString([]byte(content))}
}

func nativeCommand(t *testing.T, adapter native.Adapter, files ...contracts.SourceFile) execution.ExecutionCommand {
	t.Helper()

	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID != adapter.Name() {
			continue
		}
		req := contracts.RunRequest{
			AttemptID: adapter.Name() + "-fixture",
			Language:  contracts.LanguageSpec{ID: adapter.Name(), Version: "17"},
			Code:      contracts.CodeSpec{Files: files},
			Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
			Limits: contracts.Limits{CPUms: 60, Wallms: 500, MemoryMB: 64, PIDs: 8,
				StdoutBytes: 1024, StderrBytes: 1024, WorkspaceBytes: 5 << 20},
		}
		cmd, err := adapter.BuildCommand(t.Context(), req, languages.ResolvedLanguage{Version: language.Versions[0]})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	t.Fatal("compiled language is missing from the manifest", adapter.Name())
	return execution.ExecutionCommand{}
}
