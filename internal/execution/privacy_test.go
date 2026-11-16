package execution

import (
	"overmindv/sandbox/internal/contracts"
	"testing"
)

func TestPrivateDiagnosticsKeepOnlyTrustedCheckerOutput(t *testing.T) {
	req := contracts.RunRequest{Metadata: map[string]any{"run_mode": "graded"}, Execution: &contracts.ExecutionSpec{Mode: "execution"}}
	input := ExecutionResult{Status: contracts.StatusOK, Stdout: "SECRET", Stderr: "SECRET", Error: &contracts.RunError{Code: "internal_error", Message: "SECRET", Details: map[string]any{"private": "SECRET"}}, Cases: []ExecutionCaseResult{{Stdout: "SECRET", Stderr: "SECRET", Status: contracts.StatusOK}}}
	result := RunResultFromExecution(req, input, ResultTimingInput{}, "worker")
	if result.DiagnosticsVisibility != "private" || result.Logs.Stdout != "" || result.Logs.Stderr != "" || (result.Error != nil && (result.Error.Message == "SECRET" || result.Error.Details != nil)) || result.Cases[0].Stderr != "" {
		t.Fatalf("private diagnostics leaked: %+v", result)
	}
	if result.Cases[0].Stdout != "SECRET" || result.Cases[0].DiagnosticsVisibility != "private" {
		t.Fatal("trusted checker output missing")
	}
	compile := input
	compile.Cases = nil
	compile.Status = contracts.StatusCompileError
	compiled := RunResultFromExecution(req, compile, ResultTimingInput{}, "worker")
	if compiled.Error == nil || compiled.Error.Message == "SECRET" || compiled.Error.Details != nil || compiled.Logs.Stderr != "" {
		t.Fatal("compile diagnostics leaked")
	}
	req.Metadata["run_mode"] = "samples"
	result = RunResultFromExecution(req, input, ResultTimingInput{}, "worker")
	if result.DiagnosticsVisibility != "public" || result.Cases[0].Stderr != "SECRET" {
		t.Fatal("public samples diagnostics lost")
	}
	req.Execution.Suite = &contracts.InputSuiteReference{SuiteID: "private"}
	result = RunResultFromExecution(req, input, ResultTimingInput{}, "worker")
	if result.DiagnosticsVisibility != "private" {
		t.Fatal("suite marked public")
	}
}
