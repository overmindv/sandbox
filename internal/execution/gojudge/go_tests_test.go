package gojudge

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/gotestjob"
)

func TestCompiledGoTestsShareOneJobBudget(t *testing.T) {
	index := []gotestjob.Package{{Dir: "a/calc", ImportPath: "module/a/calc"}, {Dir: "b/calc", ImportPath: "module/b/calc"}}
	data := testBundle(t, gotestjob.BinaryName(0), index)
	compilerCalls, runnerCalls := observeRequests(t), observeRequests(t)
	compiler := httptest.NewServer(compilerCalls.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /run":
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != 1 {
				t.Error("malformed compiler request", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			cmd := req.Cmd[0]
			if !slices.Equal(cmd.Args, []string{"/usr/local/bin/sandbox-go-test-compile"}) ||
				!slices.Equal(cmd.CopyOutCached, []string{gotestjob.BundleName + "?"}) || cmd.CopyOutMax != gotestjob.MaxBundleBytes {
				t.Error("compiler profile", cmd)
			}
			_, _ = io.WriteString(w, `[{"status":"Accepted","exitStatus":0,"files":{"stdout":""},"fileIds":{"_sandbox_go_tests.tar":"ABCDEFGH"}}]`)
		case "GET /file/ABCDEFGH":
			_, _ = w.Write(data)
		case "DELETE /file/ABCDEFGH":
		default:
			http.NotFound(w, r)
		}
	})))
	defer compiler.Close()
	const outA = "{\"Action\":\"pass\",\"Package\":\"module/a/calc\",\"Test\":\"TestOne\"}\n"
	const outB = "{\"Action\":\"fail\",\"Package\":\"module/b/calc\",\"Test\":\"TestOne\"}\n"
	cases := make(chan Command, 2)
	runner := httptest.NewServer(runnerCalls.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /file":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, `"JKLMNOPQ"`)
		case "POST /run":
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Cmd) != 1 {
				t.Error("malformed runner request", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			run := req.Cmd[0]
			cases <- run
			part := Result{Status: StatusAccepted, Time: 15e6, RunTime: 25e6, Memory: 2 << 20,
				Files: map[string]string{stdoutName: outA, stderrName: "log"}}
			if run.Args[1] == "b/calc" {
				part = Result{Status: StatusNonzeroExitStatus, ExitStatus: 1, Time: 20e6, RunTime: 35e6, Memory: 1 << 20,
					Files: map[string]string{stdoutName: outB}}
			}
			_ = json.NewEncoder(w).Encode([]Result{part})
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
	cmd := compiledGoTestFixture()
	got, err := executor.Execute(t.Context(), cmd)
	if err != nil || got.Status != contracts.StatusFailed || got.TestsTotal != 2 ||
		got.FirstFailedTest != "module/b/calc::TestOne" || got.CPUms != 35 || got.RunMS != 60 || got.MemoryPeakBytes != 2<<20 {
		t.Error("unexpected compiled test result", got, err)
	}
	compilerCalls.check(t, []string{"POST /run", "GET /file/ABCDEFGH", "DELETE /file/ABCDEFGH"})
	runnerCalls.check(t, []string{"POST /file", "POST /run", "DELETE /file/JKLMNOPQ", "POST /file", "POST /run", "DELETE /file/JKLMNOPQ"})
	if len(cases) != 2 {
		t.Fatal("wanted two package invocations")
	}
	first, second := <-cases, <-cases
	if first.CPULimit != millisToNanos(100) || second.CPULimit != millisToNanos(85) ||
		first.ClockLimit != millisToNanos(200) || second.ClockLimit != millisToNanos(175) ||
		second.Files[1].Max != first.Files[1].Max-len(outA) || second.Files[2].Max != first.Files[2].Max-len("log") {
		t.Error("second package did not inherit the remaining budget")
	}
	for i, run := range []Command{first, second} {
		if run.ProcLimit != cmd.RuntimeLimits.PIDs || run.MemoryLimit != cmd.RuntimeLimits.MemoryBytes ||
			run.CopyOutMax != cmd.RuntimeLimits.WorkspaceBytes || run.CopyIn[gotestjob.RunBinaryName].FileID != "JKLMNOPQ" ||
			!slices.Equal(run.Args, []string{"/usr/local/bin/sandbox-go-test-run", index[i].Dir, index[i].ImportPath}) {
			t.Error("incorrect package runner command", i)
		}
	}
}

func TestReadGoTestBundleRejectsInvalidEntries(t *testing.T) {
	valid := []gotestjob.Package{{Dir: ".", ImportPath: "module"}}
	for _, tc := range []struct {
		name  string
		entry string
		index []gotestjob.Package
	}{
		{"unexpected artifact name", "other-0", valid},
		{"directory outside workspace", gotestjob.BinaryName(0), []gotestjob.Package{{Dir: "../other", ImportPath: "module"}}},
		{"duplicate package directory", gotestjob.BinaryName(0), append(slices.Clone(valid), gotestjob.Package{Dir: ".", ImportPath: "module/other"})},
		{"unexpected package count", gotestjob.BinaryName(0), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readGoTestBundle(testBundle(t, tc.entry, tc.index)); !errors.Is(err, ErrProtocol) {
				t.Error("invalid bundle was accepted", err)
			}
		})
	}
}

func testBundle(t *testing.T, first string, index []gotestjob.Package) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	write := func(name string, content []byte) {
		t.Helper()
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	for i := range max(1, len(index)) {
		name := gotestjob.BinaryName(i)
		if i == 0 {
			name = first
		}
		write(name, []byte("\x7fELFbinary"))
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	write(gotestjob.IndexName, data)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func compiledGoTestFixture() execution.ExecutionCommand {
	cmd := goExecutionFixture()
	cmd.Inputs = nil
	cmd.Mode = ""
	cmd.TestMode = contracts.TestModeGoTest
	cmd.Argv = []string{"go", "test", "./..."}
	cmd.RuntimeLimits.CPUms, cmd.RuntimeLimits.WallMS = 100, 200
	return cmd
}
