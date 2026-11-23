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
	"overmindv/sandbox/internal/javaclass"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/jvm"
)

func TestJavaCompilerAndRunnerReceiveOnlyTheirInputs(t *testing.T) {
	checkJVMCompilerAndRunner(t, jvm.Java(), "Main.java", "Main", "class Main {}")
}

func TestKotlinCompilerAndRunnerReceiveOnlyTheirInputs(t *testing.T) {
	checkJVMCompilerAndRunner(t, jvm.Kotlin(), "Main.kt", "MainKt", "fun main() {}")
}

func checkJVMCompilerAndRunner(t *testing.T, adapter jvm.Adapter, entry, main, source string) {
	t.Helper()
	t.Parallel()
	command := jvmCommand(t, adapter, entry, jvmFile(entry, source))
	var archive bytes.Buffer
	if err := javaclass.WriteArchive(&archive, fstest.MapFS{
		main + ".class": {Data: []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65}},
	}); err != nil {
		t.Fatal(err)
	}
	compiler := fakeJVMCompiler(t, adapter, command, archive.Bytes())
	defer compiler.Close()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /file":
			checkArtifactUpload(t, r, adapter.ArchiveName(), archive.Bytes())
			_, _ = io.WriteString(w, `"JKLMNOPQ"`)
		case "POST /run":
			checkJVMCases(t, r, adapter.ArchiveName(), command)
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":"2"}},{"status":"Accepted","exitStatus":0,"files":{"stdout":"4"}}]`)
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
	result, err := executor.Execute(t.Context(), command)
	if err != nil || result.Status != contracts.StatusOK || len(result.Cases) != 2 {
		t.Fatal("unexpected JVM result", adapter.Name(), result.Status, err)
	}
}

func TestJavaDoesNotRunWithoutValidArtifact(t *testing.T) {
	checkJVMMissingArtifact(t, jvm.Java(), "Main.java", "class Main {}")
}

func TestKotlinDoesNotRunWithoutValidArtifact(t *testing.T) {
	checkJVMMissingArtifact(t, jvm.Kotlin(), "Main.kt", "fun main() {}")
}

func checkJVMMissingArtifact(t *testing.T, adapter jvm.Adapter, entry, source string) {
	t.Helper()
	t.Parallel()
	command := jvmCommand(t, adapter, entry, jvmFile(entry, source))
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("runner was called without a valid class archive")
		http.NotFound(w, r)
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrInfrastructure) {
		t.Error("expected a missing compiler error", err)
	}
	compiler := fakeJVMCompiler(t, adapter, command, []byte("\x7fELFinvalid for JVM"))
	defer compiler.Close()
	executor, err = NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrProtocol) {
		t.Error("expected an invalid class archive error", err)
	}
}

func fakeJVMCompiler(t *testing.T, adapter jvm.Adapter, command execution.ExecutionCommand, archive []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != 1 {
				t.Error("invalid JVM compiler request", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			cmd := req.Cmd[0]
			limits := compileLimitsForJVM(adapter)
			if !slices.Equal(cmd.Args, command.CompileArgv) || len(cmd.CopyIn) != 1 || cmd.CopyIn[command.Entrypoint].Content == nil ||
				len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != "" ||
				!slices.Equal(cmd.CopyOutCached, []string{adapter.ArchiveName() + "?"}) ||
				cmd.CopyOutMax != javaclass.MaxBytes || cmd.MemoryLimit != int64(limits.memoryMiB)<<20 ||
				cmd.ClockLimit != int64(limits.wall) || cmd.CPULimit != millisToNanos(limits.cpuMS) {
				t.Error("unexpected compiler input or limits")
			}
			if err := json.NewEncoder(w).Encode([]Result{{
				Status: StatusAccepted, Files: map[string]string{stdoutName: ""},
				FileIDs: map[string]string{adapter.ArchiveName(): "ABCDEFGH"},
			}}); err != nil {
				t.Error(err)
			}
		case "GET /file/ABCDEFGH":
			_, _ = w.Write(archive)
		case "DELETE /file/ABCDEFGH":
		default:
			t.Error("unexpected compiler operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func checkArtifactUpload(t *testing.T, r *http.Request, archiveName string, want []byte) {
	t.Helper()
	reader, err := r.MultipartReader()
	if err != nil {
		t.Error(err)
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = part.Close() }()
	got, err := io.ReadAll(io.LimitReader(part, int64(len(want))+1))
	if err != nil || part.FileName() != archiveName || !bytes.Equal(got, want) {
		t.Error("runner did not receive the checked class archive", err)
	}
}

func checkJVMCases(t *testing.T, r *http.Request, archiveName string, command execution.ExecutionCommand) {
	t.Helper()
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != len(command.Inputs) {
		t.Error("invalid JVM run request", err)
		return
	}
	for i, cmd := range req.Cmd {
		file := cmd.CopyIn[archiveName]
		if len(cmd.CopyIn) != 1 || file.FileID != "JKLMNOPQ" || file.Content != nil || !slices.Equal(cmd.Args, command.Argv) ||
			len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != command.Inputs[i] ||
			len(cmd.CopyOut) != 0 || len(cmd.CopyOutCached) != 0 ||
			cmd.ProcLimit != command.RuntimeLimits.PIDs || cmd.MemoryLimit != command.RuntimeLimits.MemoryBytes ||
			cmd.CPULimit != millisToNanos(command.RuntimeLimits.CPUms) || cmd.ClockLimit != millisToNanos(command.RuntimeLimits.WallMS) {
			t.Error("unexpected case resources", i)
		}
	}
}

func jvmCommand(t *testing.T, adapter jvm.Adapter, entry string, files ...contracts.SourceFile) execution.ExecutionCommand {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == adapter.Name() {
			req := contracts.RunRequest{
				AttemptID: adapter.Name() + "-execution-fixture",
				Language:  contracts.LanguageSpec{ID: adapter.Name(), Version: language.Versions[0].Version},
				Code:      contracts.CodeSpec{Entrypoint: entry, Files: files},
				Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
				Limits: contracts.Limits{CPUms: 500, Wallms: 1000, MemoryMB: 128, PIDs: 32,
					StdoutBytes: 1024, StderrBytes: 1024, WorkspaceBytes: 1 << 20},
			}
			command, err := adapter.BuildCommand(t.Context(), req, languages.ResolvedLanguage{Version: language.Versions[0]})
			if err != nil {
				t.Fatal(err)
			}
			return command
		}
	}
	t.Fatal("JVM profile missing", adapter.Name())
	return execution.ExecutionCommand{}
}

func jvmFile(name, content string) contracts.SourceFile {
	return contracts.SourceFile{Path: name, ContentB64: base64.StdEncoding.EncodeToString([]byte(content))}
}

func TestLiveGoJudgeJava(t *testing.T) {
	main := jvmFile("pkg/Main.java", `package pkg;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Scanner;
public class Main {
    public static void main(String[] args) throws Exception {
        if (Runtime.version().feature() != 21) throw new IllegalStateException("wrong runtime");
        if (Runtime.getRuntime().maxMemory() > (64L << 20)) throw new IllegalStateException("heap limit");
        var marker = Path.of("case-only.txt");
        if (Files.exists(marker) || Files.exists(Path.of("pkg/Main.java"))) throw new IllegalStateException("extra files");
        Files.writeString(marker, "fixture");
        System.out.println(Helper.score(new Scanner(System.in).nextInt()));
    }
}`)
	helper := jvmFile("pkg/Helper.java", "package pkg; class Helper { static int score(int n) { return n * 2; } }")
	runLiveJVM(t, "SANDBOX_GOJUDGE_JAVA_INTEGRATION", jvm.Java(), []jvmScenario{
		{name: "multi-file and isolated cases", files: []contracts.SourceFile{main, helper}, status: contracts.StatusOK, output: []string{"2\n", "4\n"}},
		{name: "syntax error", files: []contracts.SourceFile{jvmFile("Main.java", "class Main {")}, status: contracts.StatusCompileError},
		{name: "entry class missing", files: []contracts.SourceFile{jvmFile("Main.java", "class Other {}")}, status: contracts.StatusCompileError},
		{name: "wall limit", files: []contracts.SourceFile{jvmFile("Main.java", `class Main {
    public static void main(String[] args) throws Exception { Thread.sleep(1500); }
}`)}, status: contracts.StatusTimeLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.WallMS = 300 }},
		{name: "output limit", files: []contracts.SourceFile{jvmFile("Main.java", `class Main {
    public static void main(String[] args) { System.out.println("01234567890123456789"); }
}`)}, status: contracts.StatusOutputLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.StdoutBytes = 8 }},
		{name: "workspace limit", files: []contracts.SourceFile{jvmFile("Main.java", "class Main {}")}, status: contracts.StatusOutputLimitExceeded,
			limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.WorkspaceBytes = 16 }},
	})
}

func TestLiveGoJudgeKotlin(t *testing.T) {
	main := jvmFile("pkg/main.kt", `package pkg
import java.nio.file.Files
import java.nio.file.Path
fun main() {
    check(Runtime.version().feature() == 21)
    check(Runtime.getRuntime().maxMemory() <= 64L * 1024 * 1024)
    val marker = Path.of("case-only.txt")
    check(!Files.exists(marker) && !Files.exists(Path.of("pkg/main.kt")))
    Files.writeString(marker, "fixture")
    println(score(readln().toInt()))
}`)
	helper := jvmFile("pkg/Helper.kt", "package pkg\nfun score(n: Int) = listOf(n, n).sum()")
	runLiveJVM(t, "SANDBOX_GOJUDGE_KOTLIN_INTEGRATION", jvm.Kotlin(), []jvmScenario{
		{name: "multi-file and isolated cases", files: []contracts.SourceFile{main, helper}, status: contracts.StatusOK, output: []string{"2\n", "4\n"}},
		{name: "syntax error", files: []contracts.SourceFile{jvmFile("Main.kt", "fun main( {")}, status: contracts.StatusCompileError},
		{name: "entry class missing", files: []contracts.SourceFile{jvmFile("Main.kt", "class Other")}, status: contracts.StatusCompileError},
		{name: "wall limit", files: []contracts.SourceFile{jvmFile("Main.kt", "fun main() { Thread.sleep(1500) }")},
			status: contracts.StatusTimeLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.WallMS = 300 }},
		{name: "output limit", files: []contracts.SourceFile{jvmFile("Main.kt", `fun main() { println("01234567890123456789") }`)},
			status: contracts.StatusOutputLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.StdoutBytes = 8 }},
		{name: "workspace limit", files: []contracts.SourceFile{jvmFile("Main.kt", "fun main() {}")},
			status: contracts.StatusOutputLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.WorkspaceBytes = 16 }},
	})
}

type jvmScenario struct {
	name   string
	files  []contracts.SourceFile
	status contracts.ExecutionStatus
	limit  func(*execution.ExecutionCommand)
	output []string
}

func runLiveJVM(t *testing.T, enabled string, adapter jvm.Adapter, scenarios []jvmScenario) {
	t.Helper()
	if os.Getenv(enabled) != "1" {
		t.Skip("requires isolated JVM compiler and runner")
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
		sources := scenarios[0].files
		command := jvmCommand(t, adapter, sources[0].Path, sources...)
		run, err := RequestFromExecutionCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		main, _ := adapter.MainClass(command.Entrypoint)
		data, verdict, err := executor.buildJVM(t.Context(), adapter, command, run.RequestID, run.Cmd[0].CopyIn, main)
		if err != nil || verdict != nil || len(data) == 0 {
			t.Fatal("compiler did not produce a class archive", verdict, err)
		}
		t.Log("compiler round trip", time.Since(start), "artifact bytes", len(data))
	})
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			command := jvmCommand(t, adapter, scenario.files[0].Path, scenario.files...)
			if scenario.limit != nil {
				scenario.limit(&command)
			}
			start := time.Now()
			result, err := executor.Execute(t.Context(), command)
			if err != nil || result.Status != scenario.status {
				t.Fatal("unexpected JVM result", adapter.Name(), result.Status, result.Error, result.Stderr, result.Cases, err)
			}
			if scenario.status == contracts.StatusCompileError &&
				(result.Error == nil || result.Error.Code != adapter.Name()+"_compile_error") {
				t.Error("unexpected compile error", result.Error)
			}
			var runMS []int
			var memory []int64
			for _, item := range result.Cases {
				runMS = append(runMS, item.DurationMS)
				memory = append(memory, item.MemoryPeakBytes)
			}
			t.Log("compile and run", time.Since(start), "case wall ms", runMS, "peak bytes", memory)
			if scenario.output != nil {
				if len(result.Cases) != len(scenario.output) {
					t.Fatal("wrong number of JVM results", len(result.Cases))
				}
				for i, want := range scenario.output {
					if result.Cases[i].Stdout != want {
						t.Error("unexpected JVM stdout", i, result.Cases[i].Stdout)
					}
				}
			}
		})
	}
}
