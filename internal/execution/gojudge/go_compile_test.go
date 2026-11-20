package gojudge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

func TestGoCompileTransfersBinaryAndPreservesRunLimits(t *testing.T) {
	artifact := []byte("\x7fELF\xff\x00artifact")
	var compileReq, runReq Request
	compilerCalls := observeRequests(t)
	runnerCalls := observeRequests(t)
	compiler := httptest.NewServer(compilerCalls.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			if err := json.NewDecoder(r.Body).Decode(&compileReq); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":""},"fileIds":{"_sandbox_go":"ABCDEFGH"}}]`)
		case "GET /file/ABCDEFGH":
			_, _ = w.Write(artifact)
		case "DELETE /file/ABCDEFGH":
		default:
			http.NotFound(w, r)
		}
	})))
	defer compiler.Close()
	runner := httptest.NewServer(runnerCalls.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /file":
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer file.Close()
			raw, err := io.ReadAll(file)
			if err != nil || !bytes.Equal(raw, artifact) || header.Filename != goArtifactName {
				t.Error("incorrect binary upload", err)
			}
			_, _ = io.WriteString(w, `"JKLMNOPQ"`)
		case "POST /run":
			if err := json.NewDecoder(r.Body).Decode(&runReq); err != nil {
				t.Error(err)
			}
			time.Sleep(70 * time.Millisecond)
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":"4"}},{"status":"Accepted","exitStatus":0,"files":{"stdout":"7"}}]`)
		case "DELETE /file/JKLMNOPQ":
		default:
			http.NotFound(w, r)
		}
	})))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	cmd := goExecutionFixture()
	cmd.RuntimeLimits.CPUms = 20
	cmd.RuntimeLimits.WallMS = 30
	got, err := executor.Execute(t.Context(), cmd)
	if err != nil || got.Status != contracts.StatusOK || len(got.Cases) != 2 {
		t.Fatal("unexpected execution outcome", got.Status, err)
	}
	if got.Cases[0].Stdout != "4" || got.Cases[1].Stdout != "7" {
		t.Error("case outputs were not preserved")
	}
	compilerCalls.check(t, []string{"POST /run", "GET /file/ABCDEFGH", "DELETE /file/ABCDEFGH"})
	runnerCalls.check(t, []string{"POST /file", "POST /run", "DELETE /file/JKLMNOPQ"})
	if len(compileReq.Cmd) != 1 || len(runReq.Cmd) != 2 {
		t.Fatal("wanted one build and two cases")
	}
	build := compileReq.Cmd[0]
	if len(build.CopyOutCached) != 1 || build.CopyOutCached[0] != goArtifactName ||
		build.CopyOutMax != maxArtifactBytes || build.CPULimit != 40000000000 || build.ClockLimit != int64(goCompileWall) ||
		!slices.Contains(build.Args, "-buildmode=exe") {
		t.Error("invalid compiler resource profile")
	}
	for i, run := range runReq.Cmd {
		bin := run.CopyIn[goArtifactName]
		src := run.CopyIn["main.go"]
		if len(run.Args) != 1 || run.Args[0] != "./"+goArtifactName || bin.FileID != "JKLMNOPQ" || bin.Content != nil ||
			src.Content == nil || run.ClockLimit != millisToNanos(cmd.RuntimeLimits.WallMS) ||
			run.CPULimit != millisToNanos(cmd.RuntimeLimits.CPUms) || run.ProcLimit != cmd.RuntimeLimits.PIDs ||
			run.MemoryLimit != cmd.RuntimeLimits.MemoryBytes || run.CopyOutMax != cmd.RuntimeLimits.WorkspaceBytes ||
			run.Files[0].Content == nil || *run.Files[0].Content != cmd.Inputs[i] ||
			run.Files[1].Max != cmd.RuntimeLimits.StdoutBytes || run.Files[2].Max != cmd.RuntimeLimits.StderrBytes {
			t.Error("case changed the original runtime limits or input", i)
		}
	}
}

func TestGoCompilerFailureDoesNotRunCases(t *testing.T) {
	compiler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"status":"Nonzero Exit Status","exitStatus":1,"files":{"stderr":"compile failed"}}]`)
	}))
	defer compiler.Close()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected request to runner", r.URL.Path)
	}))
	defer runner.Close()
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner.URL, CompilerURL: compiler.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := executor.Execute(t.Context(), goExecutionFixture())
	if err != nil || got.Status != contracts.StatusCompileError || len(got.Cases) != 0 {
		t.Error("unexpected compiler verdict", got.Status, err)
	}
}

func TestGoPhaseTimeoutIsInfrastructureError(t *testing.T) {
	err := phaseError(t.Context(), context.DeadlineExceeded, "Go compiler request")
	if !errors.Is(err, ErrInfrastructure) || errors.Is(err, context.DeadlineExceeded) {
		t.Error("local phase timeout should not become a user verdict", err)
	}
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	err = phaseError(parent, context.Canceled, "Go runner request")
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrInfrastructure) {
		t.Error("parent cancellation should be preserved", err)
	}
}

func TestArtifactRejectsInvalidIDAndOversizedBody(t *testing.T) {
	check := observeRequests(t)
	srv := httptest.NewServer(check.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8388609")
	})))
	defer srv.Close()
	client, err := NewClient(config.GoJudgeClient{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../../bad", "ZZZZZZZZ"} {
		if _, err := client.getArtifact(t.Context(), id); !errors.Is(err, ErrProtocol) {
			t.Error("wanted artifact protocol error", err)
		}
	}
	check.check(t, []string{"GET /file/ZZZZZZZZ"})
}

func TestFileSessionDoesNotReconnect(t *testing.T) {
	check := observeRequests(t)
	srv := httptest.NewServer(check.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, `"ABCDEFGH"`)
	})))
	defer srv.Close()
	client, err := NewClient(config.GoJudgeClient{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	session, closeSession := client.fileSession()
	defer closeSession()
	id, err := session.putArtifact(t.Context(), []byte("\x7fELF"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.getArtifact(t.Context(), id); !errors.Is(err, ErrInfrastructure) {
		t.Error("reused a file ID after its connection closed", err)
	}
	check.check(t, []string{"POST /file"})
}

func TestLiveGoJudgeCompilesOnceForMultipleCases(t *testing.T) {
	compiler := os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	runner := os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" || compiler == "" || runner == "" {
		t.Skip("requires a go-judge compiler and runner")
	}
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: runner, CompilerURL: compiler, RequestTimeoutMS: 10000})
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int{0, 10} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			t.Parallel()
			cmd := goExecutionFixture()
			cmd.AttemptID = t.Name()
			cmd.Files[1].Content = fmt.Appendf(nil, "package main; import \"fmt\"; func main() { var a,b int; fmt.Scan(&a,&b); fmt.Println(a+b+%d) }", delta)
			result, err := executor.Execute(t.Context(), cmd)
			if err != nil || result.Status != contracts.StatusOK || len(result.Cases) != 2 {
				t.Fatal("unexpected live execution outcome", result.Status, err, result.Error)
			}
			if result.Cases[0].Stdout != fmt.Sprintln(4+delta) || result.Cases[1].Stdout != fmt.Sprintln(7+delta) {
				t.Error("incorrect case outputs")
			}
		})
	}
	t.Run("non_executable", func(t *testing.T) {
		cmd := goExecutionFixture()
		cmd.AttemptID = t.Name()
		cmd.Files[1].Content = []byte("package smoke; func Add(a,b int) int { return a+b }")
		result, err := executor.Execute(t.Context(), cmd)
		if err != nil || result.Status != contracts.StatusCompileError {
			t.Error("wanted compile_error for non-executable package", result.Status, err)
		}
	})
}

func BenchmarkLiveGoJudgeGoCases(b *testing.B) {
	compiler := os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	runner := os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	if os.Getenv("SANDBOX_GOJUDGE_BENCH") != "1" || compiler == "" || runner == "" {
		b.Skip("requires a go-judge compiler and runner")
	}
	for _, name := range []string{"rebuild_per_case", "compile_once"} {
		b.Run(name, func(b *testing.B) {
			cfg := config.GoJudgeClient{BaseURL: runner, CompilerURL: compiler, RequestTimeoutMS: 70000}
			cmd := goExecutionFixture()
			cmd.Inputs = []string{"1 1\n", "2 2\n", "3 3\n", "4 4\n"}
			if name == "rebuild_per_case" {
				cfg.BaseURL, cfg.CompilerURL = compiler, ""
				cmd.RuntimeLimits.CPUms = 40000
				cmd.RuntimeLimits.WallMS = 65000
				cmd.RuntimeLimits.MemoryBytes = 768 << 20
				cmd.RuntimeLimits.PIDs = 64
				cmd.Environment = map[string]string{
					"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "GOMAXPROCS": "2",
					"CGO_ENABLED": "0", "GOCACHE": "/tmp/go-build", "GOMODCACHE": "/tmp/go-mod",
					"GOPROXY": "off", "GOTOOLCHAIN": "local",
				}
			}
			executor, err := NewExecutor(cfg)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				result, err := executor.Execute(b.Context(), cmd)
				if err != nil || result.Status != contracts.StatusOK || len(result.Cases) != len(cmd.Inputs) {
					b.Fatal("could not finish all four cases", result.Status, err, result.Error)
				}
			}
		})
	}
}

func goExecutionFixture() execution.ExecutionCommand {
	return execution.ExecutionCommand{
		Mode: contracts.ExecutionMode, Adapter: "go", AttemptID: "go-multi",
		Argv: []string{"go", "run", "."}, Inputs: []string{"2 2\n", "3 4\n"},
		Files: []execution.ExecutionFile{
			{Path: "go.mod", Kind: execution.FileKindBuild, Content: []byte("module smoke\n\ngo 1.26\n")},
			{Path: "main.go", Kind: execution.FileKindSource, Content: []byte("package main; import \"fmt\"; func main() { var a,b int; fmt.Scan(&a,&b); fmt.Println(a+b) }")},
		},
		RuntimeLimits: execution.RuntimeLimits{
			CPUms: 2000, WallMS: 5000, MemoryBytes: 128 << 20, PIDs: 32,
			StdoutBytes: 32768, StderrBytes: 32768, WorkspaceBytes: 1 << 20,
		},
		CreatedAt: time.Now(),
	}
}

type requestRecorder struct {
	mu   sync.Mutex
	log  []string
	addr map[string]bool
}

func observeRequests(t *testing.T) *requestRecorder {
	t.Helper()
	return &requestRecorder{addr: make(map[string]bool)}
}

func (c *requestRecorder) wrap(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.log = append(c.log, r.Method+" "+r.URL.Path)
		c.addr[r.RemoteAddr] = true
		handler.ServeHTTP(w, r)
	})
}

func (c *requestRecorder) check(t *testing.T, want []string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Equal(c.log, want) || len(c.addr) != 1 {
		t.Error("unexpected requests or connection count", c.log, len(c.addr))
	}
}
