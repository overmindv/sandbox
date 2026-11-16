package execution

import (
	"overmindv/sandbox/internal/contracts"
	"testing"
)

func TestExecutionRejectsTestMaterial(t *testing.T) {
	req := contracts.RunRequest{Execution: &contracts.ExecutionSpec{Mode: "execution", Inputs: []string{"x"}}, Tests: contracts.TestSpec{Files: []contracts.SourceFile{{Path: "expected.txt", ContentB64: "NDI="}}}}
	if _, err := CommandFromRunRequest(req); err == nil {
		t.Fatal("test material crossed execution boundary")
	}
}
