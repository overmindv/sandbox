package gojudge

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/dotnetjob"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/csharp"
)

func TestCSharpRejectsMissingCompilerOrInvalidAssembly(t *testing.T) {
	t.Parallel()
	command := csharpCommand(t, csFile("Program.cs", "System.Console.WriteLine(1);"))
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("runner was called without a valid C# assembly")
		http.NotFound(w, r)
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrInfrastructure) {
		t.Error("expected missing C# compiler", err)
	}
	compiler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			checkCSharpCompilerRequest(t, r, command)
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":""},"fileIds":{"_sandbox_csharp.dll":"ABCDEFGH"}}]`)
		case "GET /file/ABCDEFGH":
			_, _ = io.WriteString(w, "MZ")
		case "DELETE /file/ABCDEFGH":
		default:
			t.Error("unexpected compiler operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer compiler.Close()
	executor, err = NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrProtocol) {
		t.Error("expected invalid C# assembly", err)
	}
}

func checkCSharpCompilerRequest(t *testing.T, r *http.Request, command execution.ExecutionCommand) {
	t.Helper()
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != 1 {
		t.Error("invalid compiler request", err)
		return
	}
	cmd := req.Cmd[0]
	if !slices.Equal(cmd.Args, command.CompileArgv) || len(cmd.CopyIn) != 1 ||
		len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != "" ||
		!slices.Equal(cmd.CopyOutCached, []string{dotnetjob.AssemblyName + "?"}) ||
		cmd.CopyOutMax != dotnetjob.MaxBytes || cmd.MemoryLimit != 512<<20 || cmd.ClockLimit != int64(csharpCompileWall) {
		t.Error("unexpected compiler inputs or limits")
	}
	if got := cmd.CopyIn["Program.cs"].Content; got == nil || *got != string(command.Files[0].Content) {
		t.Error("compiler did not receive the source")
	}
}

func csFile(name, source string) contracts.SourceFile {
	return contracts.SourceFile{Path: name, ContentB64: base64.StdEncoding.EncodeToString([]byte(source))}
}

func csharpCommand(t *testing.T, files ...contracts.SourceFile) execution.ExecutionCommand {
	t.Helper()
	req := contracts.RunRequest{
		AttemptID: "csharp-execution-fixture",
		Code:      contracts.CodeSpec{Entrypoint: files[0].Path, Files: files},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
		Limits:    contracts.Limits{CPUms: 500, Wallms: 1000, MemoryMB: 128, PIDs: 32, StdoutBytes: 1024, StderrBytes: 1024, WorkspaceBytes: 1 << 20},
	}
	cmd, err := (csharp.Adapter{}).BuildCommand(t.Context(), req, languages.ResolvedLanguage{})
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestLiveGoJudgeCSharp(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_CSHARP_INTEGRATION") != "1" {
		t.Skip("requires isolated C# compiler and runner")
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
	files := []contracts.SourceFile{
		csFile("src/Program.cs", `using System;
using System.Globalization;
using System.IO;
if (Environment.Version != new Version(10, 0, 15)) throw new Exception("wrong runtime");
if (GC.GetGCMemoryInfo().TotalAvailableMemoryBytes > 64L * 1024 * 1024) throw new Exception("heap limit");
if (CultureInfo.GetCultureInfo("ru-RU").Name != "ru-RU") throw new Exception("missing ICU");
if (File.Exists("case-only.txt") || File.Exists("src/Program.cs")) throw new Exception("extra files");
File.WriteAllText("case-only.txt", "fixture");
Console.WriteLine(Helper.Score(int.Parse(Console.ReadLine()!)));`),
		csFile("src/Helper.cs", `internal static class Helper {
    private static int Factor { get; set => field = value; } = 2;
    public static int Score(int n) => n * Factor;
}`),
	}
	t.Run("compile time", func(t *testing.T) {
		cmd := csharpCommand(t, files...)
		run, err := RequestFromExecutionCommand(cmd)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		assembly, verdict, err := executor.buildCSharp(t.Context(), cmd, run.RequestID, run.Cmd[0].CopyIn)
		if err != nil || verdict != nil || len(assembly) == 0 {
			t.Fatal("compiler did not produce an assembly", verdict, err)
		}
		t.Log("compiler round trip", time.Since(start), "artifact bytes", len(assembly))
	})
	for _, sc := range []struct {
		name   string
		files  []contracts.SourceFile
		status contracts.ExecutionStatus
		limit  func(*execution.RuntimeLimits)
	}{
		{name: "multi-file and isolated cases", files: files, status: contracts.StatusOK},
		{name: "syntax error", files: []contracts.SourceFile{csFile("Program.cs", "System.Console.WriteLine(")}, status: contracts.StatusCompileError},
		{name: "type error", files: []contracts.SourceFile{csFile("Program.cs", `int value = "not a number";`)}, status: contracts.StatusCompileError},
		{name: "missing library", files: []contracts.SourceFile{csFile("Program.cs", "using NotInstalled; System.Console.WriteLine(1);")},
			status: contracts.StatusCompileError},
		{name: "wall limit", files: []contracts.SourceFile{csFile("Program.cs", "System.Threading.Thread.Sleep(1500);")},
			status: contracts.StatusTimeLimitExceeded, limit: func(l *execution.RuntimeLimits) { l.WallMS = 300 }},
		{name: "output limit", files: []contracts.SourceFile{csFile("Program.cs", `System.Console.WriteLine("01234567890123456789");`)},
			status: contracts.StatusOutputLimitExceeded, limit: func(l *execution.RuntimeLimits) { l.StdoutBytes = 8 }},
		{name: "workspace limit", files: []contracts.SourceFile{csFile("Program.cs", "System.Console.WriteLine(1);")},
			status: contracts.StatusOutputLimitExceeded, limit: func(l *execution.RuntimeLimits) { l.WorkspaceBytes = 16 }},
		{name: "assembly size limit", files: []contracts.SourceFile{csFile("Program.cs",
			`System.Console.WriteLine("`+strings.Repeat("a", 520<<10)+`");`)}, status: contracts.StatusOutputLimitExceeded},
	} {
		t.Run(sc.name, func(t *testing.T) {
			cmd := csharpCommand(t, sc.files...)
			if sc.limit != nil {
				sc.limit(&cmd.RuntimeLimits)
			}
			start := time.Now()
			result, err := executor.Execute(t.Context(), cmd)
			if err != nil || result.Status != sc.status {
				t.Fatal("unexpected C# result", result.Status, result.Error, result.Stderr, result.Cases, err)
			}
			if sc.status == contracts.StatusCompileError && (result.Error == nil || result.Error.Code != "csharp_compile_error") {
				t.Error("unexpected C# compile error", result.Error)
			}
			if sc.limit == nil && sc.status == contracts.StatusOutputLimitExceeded &&
				(result.Error == nil || result.Error.Code != "csharp_artifact_too_large") {
				t.Error("unexpected C# artifact error", result.Error)
			}
			if sc.status == contracts.StatusOK {
				if len(result.Cases) != 2 || result.Cases[0].Stdout != "2\n" || result.Cases[1].Stdout != "4\n" {
					t.Fatal("incorrect case outputs", result.Cases)
				}
				t.Log("case wall ms", result.Cases[0].DurationMS, result.Cases[1].DurationMS, "peak bytes", result.MemoryPeakBytes)
			}
			t.Log("compile and run", time.Since(start))
		})
	}
}
