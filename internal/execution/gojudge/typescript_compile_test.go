package gojudge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/javascript"
	"overmindv/sandbox/internal/languages/typescript"
	"overmindv/sandbox/internal/tsjob"
)

func TestTypeScriptCompilerAndRunnerOnlyReceiveTheirInputs(t *testing.T) {
	t.Parallel()
	command := tsCommand(t, tsFile("main.ts", "console.log(1)"))
	archive := tsArchive(t, fstest.MapFS{"main.js": {Data: []byte("console.log(1)")}})
	compiler := fakeTSCompiler(t, command, archive)
	defer compiler.Close()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /file":
			checkArtifactUpload(t, r, tsjob.BundleName, archive)
			_, _ = io.WriteString(w, `"JKLMNOPQ"`)
		case "POST /run":
			checkTSCases(t, r, command)
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":"1"}},{"status":"Accepted","exitStatus":0,"files":{"stdout":"1"}}]`)
		case "DELETE /file/JKLMNOPQ":
		default:
			t.Error("unexpected runner operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := executor.Execute(t.Context(), command)
	if err != nil || got.Status != contracts.StatusOK || len(got.Cases) != 2 {
		t.Fatal("unexpected TypeScript result", got.Status, err)
	}
}

func TestTypeScriptRejectsMissingCompilerOrUnexpectedArtifact(t *testing.T) {
	t.Parallel()
	command := tsCommand(t, tsFile("main.ts", "console.log(1)"), tsFile("helper.ts", "export const value = 1"))
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("runner was called without a valid TypeScript bundle")
		http.NotFound(w, r)
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrInfrastructure) {
		t.Error("expected missing TypeScript compiler", err)
	}
	archive := tsArchive(t, fstest.MapFS{"main.js": {}, "unrelated.js": {}})
	compiler := fakeTSCompiler(t, command, archive)
	defer compiler.Close()
	executor, err = NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrProtocol) {
		t.Error("expected an invalid output file set", err)
	}
}

func tsArchive(t *testing.T, files fstest.MapFS) []byte {
	t.Helper()
	var archive bytes.Buffer
	if err := tsjob.WriteBundle(&archive, files); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func fakeTSCompiler(t *testing.T, command execution.ExecutionCommand, archive []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != 1 {
				t.Error("invalid compiler request", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			cmd := req.Cmd[0]
			if !slices.Equal(cmd.Args, command.CompileArgv) || len(cmd.CopyIn) != len(command.Files) ||
				len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != "" ||
				!slices.Equal(cmd.CopyOutCached, []string{tsjob.BundleName + "?"}) ||
				cmd.CopyOutMax != tsjob.MaxBundleBytes || cmd.MemoryLimit != 512<<20 || cmd.ClockLimit != int64(tsCompileWall) {
				t.Error("compiler got unexpected inputs or limits")
			}
			for _, file := range command.Files {
				if got := cmd.CopyIn[file.Path].Content; got == nil || *got != string(file.Content) {
					t.Error("compiler source missing", file.Path)
				}
			}
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":""},"fileIds":{"_sandbox_ts.tar":"ABCDEFGH"}}]`)
		case "GET /file/ABCDEFGH":
			_, _ = w.Write(archive)
		case "DELETE /file/ABCDEFGH":
		default:
			t.Error("unexpected compiler operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func checkTSCases(t *testing.T, r *http.Request, command execution.ExecutionCommand) {
	t.Helper()
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != len(command.Inputs) {
		t.Error("invalid TypeScript run request", err)
		return
	}
	for i, cmd := range req.Cmd {
		artifact := cmd.CopyIn[tsjob.BundleName]
		if len(cmd.CopyIn) != 1 || artifact.FileID != "JKLMNOPQ" || artifact.Content != nil ||
			!slices.Equal(cmd.Args, command.Argv) ||
			len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != command.Inputs[i] ||
			cmd.MemoryLimit != command.RuntimeLimits.MemoryBytes || cmd.ProcLimit != command.RuntimeLimits.PIDs {
			t.Error("unexpected runtime case inputs", i)
		}
	}
}

func tsFile(name, source string) contracts.SourceFile {
	return contracts.SourceFile{Path: name, ContentB64: base64.StdEncoding.EncodeToString([]byte(source))}
}

func tsCommand(t *testing.T, files ...contracts.SourceFile) execution.ExecutionCommand {
	t.Helper()
	command, err := (typescript.Adapter{}).BuildCommand(t.Context(), tsRequest(files), languages.ResolvedLanguage{})
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func tsRequest(files []contracts.SourceFile) contracts.RunRequest {
	return contracts.RunRequest{
		AttemptID: "typescript-execution-fixture",
		Code:      contracts.CodeSpec{Entrypoint: files[0].Path, Files: files},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
		Limits:    contracts.Limits{CPUms: 500, Wallms: 1000, MemoryMB: 128, PIDs: 32, StdoutBytes: 1024, StderrBytes: 1024, WorkspaceBytes: 1 << 20},
	}
}

func TestLiveGoJudgeTypeScript(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_TYPESCRIPT_INTEGRATION") != "1" {
		t.Skip("requires isolated TypeScript compiler and runner")
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
	sources := []contracts.SourceFile{
		tsFile("src/main.ts", `import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { score } from "./helper";
if (existsSync("case-only.txt") || existsSync("src/main.ts")) {
    throw new Error("unexpected workspace files");
}
writeFileSync("case-only.txt", "fixture");
console.log(score(Number(readFileSync(0, "utf8"))));`),
		tsFile("src/helper.ts", `enum Multiplier { Double = 2 }
export function score(value: number): number { return value * Multiplier.Double; }`),
	}
	var emitted []tsjob.File
	if !t.Run("compile time", func(t *testing.T) {
		cmd := tsCommand(t, sources...)
		req, err := RequestFromExecutionCommand(cmd)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		bundle, verdict, err := executor.buildTypeScript(t.Context(), cmd, req.RequestID, req.Cmd[0].CopyIn)
		if err != nil || verdict != nil {
			t.Fatal("compile fixture did not produce a bundle", verdict, err)
		}
		emitted, err = tsjob.ReadBundle(bundle, "src/main.js")
		if err != nil {
			t.Fatal(err)
		}
		t.Log("compiler round trip", time.Since(start), "artifact bytes", len(bundle))
	}) {
		return
	}
	t.Run("javascript baseline", func(t *testing.T) { checkTSJavaScriptBaseline(t, executor, emitted) })
	for _, sc := range []struct {
		name   string
		files  []contracts.SourceFile
		status contracts.ExecutionStatus
		limit  func(*execution.RuntimeLimits)
	}{
		{name: "multi-file and isolated cases", files: sources, status: contracts.StatusOK},
		{name: "syntax error", files: []contracts.SourceFile{tsFile("main.ts", "function main( {")}, status: contracts.StatusCompileError},
		{name: "type error", files: []contracts.SourceFile{tsFile("main.ts", `const n: number = "not a number"; console.log(n);`)}, status: contracts.StatusCompileError},
		{name: "missing package", files: []contracts.SourceFile{tsFile("main.ts", `import { run } from "not-installed"; run();`)}, status: contracts.StatusCompileError},
		{name: "wall limit", files: []contracts.SourceFile{tsFile("main.ts", "setTimeout(() => console.log(1), 1500);")},
			status: contracts.StatusTimeLimitExceeded, limit: func(l *execution.RuntimeLimits) { l.WallMS = 300 }},
		{name: "output limit", files: []contracts.SourceFile{tsFile("main.ts", `console.log("01234567890123456789");`)},
			status: contracts.StatusOutputLimitExceeded, limit: func(l *execution.RuntimeLimits) { l.StdoutBytes = 8 }},
		{name: "workspace limit", files: []contracts.SourceFile{tsFile("main.ts", "console.log(1);")},
			status: contracts.StatusOutputLimitExceeded, limit: func(l *execution.RuntimeLimits) { l.WorkspaceBytes = 16 }},
	} {
		t.Run(sc.name, func(t *testing.T) {
			cmd := tsCommand(t, sc.files...)
			if sc.limit != nil {
				sc.limit(&cmd.RuntimeLimits)
			}
			start := time.Now()
			result, err := executor.Execute(t.Context(), cmd)
			if err != nil || result.Status != sc.status {
				t.Fatal("unexpected TypeScript result", result.Status, result.Error, result.Stderr, result.Cases, err)
			}
			if sc.status == contracts.StatusOK {
				checkTSOutputs(t, result)
			}
			if sc.status == contracts.StatusCompileError && (result.Error == nil || result.Error.Code != "typescript_compile_error") {
				t.Error("unexpected TypeScript compile error", result.Error)
			}
			t.Log("compile and run", time.Since(start))
		})
	}
}

func checkTSJavaScriptBaseline(t *testing.T, executor *Executor, emitted []tsjob.File) {
	t.Helper()
	files := make([]contracts.SourceFile, 0, len(emitted))
	for _, file := range emitted {
		files = append(files, tsFile(file.Path, string(file.Content)))
	}
	req := tsRequest(files)
	req.Code.Entrypoint = "src/main.js"
	resolved := languages.ResolvedLanguage{Version: config.LanguageVersionConfig{
		FilePolicy: config.LanguageFilePolicy{AllowedExtensions: []string{".js"}},
	}}
	cmd, err := javascript.NewAdapter().BuildCommand(t.Context(), req, resolved)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), cmd)
	if err != nil || result.Status != contracts.StatusOK {
		t.Fatal("JavaScript regression", result.Status, result.Cases, err)
	}
	checkTSOutputs(t, result)
}

func checkTSOutputs(t *testing.T, result execution.ExecutionResult) {
	t.Helper()
	if len(result.Cases) != 2 || result.Cases[0].Stdout != "2\n" || result.Cases[1].Stdout != "4\n" {
		t.Fatal("incorrect case outputs", result.Cases)
	}
	t.Log("case wall ms", result.Cases[0].DurationMS, result.Cases[1].DurationMS, "peak bytes", result.MemoryPeakBytes)
}
