package gojudge

import (
	"reflect"
	"strings"
	"testing"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

func TestRequestFromExecutionCommandMapsCopyInCollectorsAndLimits(t *testing.T) {
	command := validGoJudgeCommand()

	request, err := RequestFromExecutionCommand(command)
	if err != nil {
		t.Errorf("map request: %v", err)
		return
	}

	if request.RequestID != command.AttemptID {
		t.Errorf("expected request id %q, got %q", command.AttemptID, request.RequestID)
	}
	if len(request.Cmd) != 1 {
		t.Errorf("expected one go-judge command, got %d", len(request.Cmd))
		return
	}

	cmd := request.Cmd[0]
	if !reflect.DeepEqual(cmd.Args, command.Argv) {
		t.Errorf("expected argv %#v, got %#v", command.Argv, cmd.Args)
	}
	if !containsEnv(cmd.Env, "PATH=") {
		t.Errorf("expected PATH env, got %#v", cmd.Env)
	}
	if cmd.CopyIn["solution.py"].Content != "def add(a, b):\n    return a + b\n" {
		t.Errorf("expected solution.py copyIn, got %#v", cmd.CopyIn)
	}
	if !containsString(cmd.CopyOut, ".sandbox/pytest-report.json?") {
		t.Errorf("expected optional pytest report copyOut, got %#v", cmd.CopyOut)
	}
	if len(cmd.Files) != 3 || cmd.Files[1].Name != stdoutName || cmd.Files[2].Name != stderrName {
		t.Errorf("expected stdin/stdout/stderr file descriptors, got %#v", cmd.Files)
	}
	if cmd.CPULimit != 2_000_000_000 {
		t.Errorf("expected cpu ns, got %d", cmd.CPULimit)
	}
	if cmd.ClockLimit != 5_000_000_000 {
		t.Errorf("expected clock ns, got %d", cmd.ClockLimit)
	}
	if cmd.MemoryLimit != 128*1024*1024 {
		t.Errorf("expected memory bytes, got %d", cmd.MemoryLimit)
	}
	if cmd.ProcLimit != 32 {
		t.Errorf("expected proc limit, got %d", cmd.ProcLimit)
	}
}

func TestRequestFromExecutionCommandRejectsUnsafeCopyIn(t *testing.T) {
	command := validGoJudgeCommand()
	command.Files[0].Path = "../solution.py"

	_, err := RequestFromExecutionCommand(command)
	if err == nil {
		t.Error("expected unsafe copyIn path to be rejected")
	}
}

func TestRequestFromExecutionCommandRejectsEmptyArgv(t *testing.T) {
	command := validGoJudgeCommand()
	command.Argv = nil

	_, err := RequestFromExecutionCommand(command)
	if err == nil {
		t.Error("expected missing argv error")
	}
}

func containsEnv(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validGoJudgeCommand() execution.ExecutionCommand {
	return execution.ExecutionCommand{
		ID:        "22222222-2222-4222-8222-222222222222",
		AttemptID: "22222222-2222-4222-8222-222222222222",
		Language:  contracts.LanguageSpec{ID: "python", Version: "3.12"},
		Adapter:   "python",
		Argv: []string{
			"python3",
			"-m",
			"pytest",
			"-q",
			"--disable-warnings",
			"--tb=short",
			"-p",
			"_sandbox_pytest_plugin",
			"test_solution.py",
		},
		Environment: map[string]string{
			"PYTHONDONTWRITEBYTECODE": "1",
			"SANDBOX_PYTEST_REPORT":   ".sandbox/pytest-report.json",
		},
		CopyOut: []string{".sandbox/pytest-report.json"},
		Files: []execution.ExecutionFile{
			{
				Path:    "solution.py",
				Kind:    execution.FileKindSource,
				Content: []byte("def add(a, b):\n    return a + b\n"),
			},
			{
				Path:    "test_solution.py",
				Kind:    execution.FileKindTest,
				Content: []byte("from solution import add\n"),
			},
			{
				Path:    "_sandbox_pytest_plugin.py",
				Kind:    execution.FileKindHarness,
				Content: []byte(""),
			},
		},
		TestMode: contracts.TestModePytest,
		RuntimeLimits: execution.RuntimeLimits{
			CPUms:          2000,
			WallMS:         5000,
			MemoryBytes:    128 * 1024 * 1024,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
	}
}
