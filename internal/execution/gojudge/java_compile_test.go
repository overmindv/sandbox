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
	"overmindv/sandbox/internal/languages/java"
)

func TestJavaCompilerAndRunnerReceiveOnlyTheirInputs(t *testing.T) {
	t.Parallel()
	command := javaCommand(t, "Main.java", javaFile("Main.java", "class Main {}"))
	var archive bytes.Buffer
	if err := javaclass.WriteArchive(&archive, fstest.MapFS{
		"Main.class": {Data: []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 65}},
	}); err != nil {
		t.Fatal(err)
	}
	compiler := fakeJavaCompiler(t, command, archive.Bytes())
	defer compiler.Close()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /file":
			checkJavaUpload(t, r, archive.Bytes())
			_, _ = io.WriteString(w, `"JKLMNOPQ"`)
		case "POST /run":
			checkJavaCases(t, r, command)
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
		t.Fatal("unexpected Java result", result.Status, err)
	}
}

func TestJavaDoesNotRunWithoutValidArtifact(t *testing.T) {
	t.Parallel()
	command := javaCommand(t, "Main.java", javaFile("Main.java", "class Main {}"))
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("runner was called without a valid Java archive")
		http.NotFound(w, r)
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrInfrastructure) {
		t.Error("expected a missing Java compiler error", err)
	}
	compiler := fakeJavaCompiler(t, command, []byte("\x7fELFinvalid for Java"))
	defer compiler.Close()
	executor, err = NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), command); !errors.Is(err, ErrProtocol) {
		t.Error("expected an invalid Java archive error", err)
	}
}

func fakeJavaCompiler(t *testing.T, command execution.ExecutionCommand, archive []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != 1 {
				t.Error("invalid Java compiler request", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			cmd := req.Cmd[0]
			if !slices.Equal(cmd.Args, command.CompileArgv) || len(cmd.CopyIn) != 1 || cmd.CopyIn["Main.java"].Content == nil ||
				len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != "" ||
				!slices.Equal(cmd.CopyOutCached, []string{javaclass.ArchiveName + "?"}) ||
				cmd.CopyOutMax != javaclass.MaxBytes || cmd.MemoryLimit != 512<<20 || cmd.ClockLimit != int64(javaCompileWall) {
				t.Error("unexpected compiler input or limits")
			}
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":""},"fileIds":{"_sandbox_java.jar":"ABCDEFGH"}}]`)
		case "GET /file/ABCDEFGH":
			_, _ = w.Write(archive)
		case "DELETE /file/ABCDEFGH":
		default:
			t.Error("unexpected compiler operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func checkJavaUpload(t *testing.T, r *http.Request, want []byte) {
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
	got, err := io.ReadAll(io.LimitReader(part, javaclass.MaxBytes+1))
	if err != nil || part.FileName() != javaclass.ArchiveName || !bytes.Equal(got, want) {
		t.Error("runner did not receive the checked class archive", err)
	}
}

func checkJavaCases(t *testing.T, r *http.Request, command execution.ExecutionCommand) {
	t.Helper()
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != len(command.Inputs) {
		t.Error("invalid Java run request", err)
		return
	}
	for i, cmd := range req.Cmd {
		file := cmd.CopyIn[javaclass.ArchiveName]
		if len(cmd.CopyIn) != 1 || file.FileID != "JKLMNOPQ" || file.Content != nil || !slices.Equal(cmd.Args, command.Argv) ||
			len(cmd.Files) != 3 || cmd.Files[0].Content == nil || *cmd.Files[0].Content != command.Inputs[i] ||
			len(cmd.CopyOut) != 0 || len(cmd.CopyOutCached) != 0 ||
			cmd.ProcLimit != command.RuntimeLimits.PIDs || cmd.MemoryLimit != command.RuntimeLimits.MemoryBytes ||
			cmd.CPULimit != millisToNanos(command.RuntimeLimits.CPUms) || cmd.ClockLimit != millisToNanos(command.RuntimeLimits.WallMS) {
			t.Error("unexpected case resources", i)
		}
	}
}

func javaCommand(t *testing.T, entry string, files ...contracts.SourceFile) execution.ExecutionCommand {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == java.AdapterName {
			req := contracts.RunRequest{
				AttemptID: "java-execution-fixture",
				Language:  contracts.LanguageSpec{ID: java.AdapterName, Version: "21"},
				Code:      contracts.CodeSpec{Entrypoint: entry, Files: files},
				Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
				Limits: contracts.Limits{CPUms: 500, Wallms: 1000, MemoryMB: 128, PIDs: 32,
					StdoutBytes: 1024, StderrBytes: 1024, WorkspaceBytes: 1 << 20},
			}
			command, err := (java.Adapter{}).BuildCommand(t.Context(), req, languages.ResolvedLanguage{Version: language.Versions[0]})
			if err != nil {
				t.Fatal(err)
			}
			return command
		}
	}
	t.Fatal("Java profile missing")
	return execution.ExecutionCommand{}
}

func javaFile(name, content string) contracts.SourceFile {
	return contracts.SourceFile{Path: name, ContentB64: base64.StdEncoding.EncodeToString([]byte(content))}
}

func TestLiveGoJudgeJava(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_JAVA_INTEGRATION") != "1" {
		t.Skip("requires isolated Java compiler and runner")
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
	main := javaFile("pkg/Main.java", `package pkg;
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
	helper := javaFile("pkg/Helper.java", "package pkg; class Helper { static int score(int n) { return n * 2; } }")
	t.Run("compile time", func(t *testing.T) {
		command := javaCommand(t, main.Path, main, helper)
		run, err := RequestFromExecutionCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		data, verdict, err := executor.buildJava(t.Context(), command, run.RequestID, run.Cmd[0].CopyIn, "pkg.Main")
		if err != nil || verdict != nil || len(data) == 0 {
			t.Fatal("Java compile did not produce an archive", verdict, err)
		}
		t.Log("compiler round trip", time.Since(start), "artifact bytes", len(data))
	})
	for _, scenario := range []struct {
		name   string
		files  []contracts.SourceFile
		status contracts.ExecutionStatus
		limit  func(*execution.ExecutionCommand)
		output []string
	}{
		{name: "multi-file and isolated cases", files: []contracts.SourceFile{main, helper}, status: contracts.StatusOK, output: []string{"2\n", "4\n"}},
		{name: "syntax error", files: []contracts.SourceFile{javaFile("Main.java", "class Main {")}, status: contracts.StatusCompileError},
		{name: "entry class missing", files: []contracts.SourceFile{javaFile("Main.java", "class Other {}")}, status: contracts.StatusCompileError},
		{name: "wall limit", files: []contracts.SourceFile{javaFile("Main.java", `class Main {
    public static void main(String[] args) throws Exception { Thread.sleep(1500); }
}`)}, status: contracts.StatusTimeLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.WallMS = 300 }},
		{name: "output limit", files: []contracts.SourceFile{javaFile("Main.java", `class Main {
    public static void main(String[] args) { System.out.println("01234567890123456789"); }
}`)}, status: contracts.StatusOutputLimitExceeded, limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.StdoutBytes = 8 }},
		{name: "workspace limit", files: []contracts.SourceFile{javaFile("Main.java", "class Main {}")}, status: contracts.StatusOutputLimitExceeded,
			limit: func(cmd *execution.ExecutionCommand) { cmd.RuntimeLimits.WorkspaceBytes = 16 }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			command := javaCommand(t, scenario.files[0].Path, scenario.files...)
			if scenario.limit != nil {
				scenario.limit(&command)
			}
			start := time.Now()
			result, err := executor.Execute(t.Context(), command)
			if err != nil || result.Status != scenario.status {
				t.Fatal("unexpected Java result", result.Status, result.Error, result.Stderr, result.Cases, err)
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
					t.Fatal("wrong number of Java results", len(result.Cases))
				}
				for i, want := range scenario.output {
					if result.Cases[i].Stdout != want {
						t.Error("unexpected Java stdout", i, result.Cases[i].Stdout)
					}
				}
			}
		})
	}
}
