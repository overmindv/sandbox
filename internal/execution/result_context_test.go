package execution

import (
	"encoding/json"
	"overmindv/sandbox/internal/contracts"
	"testing"
)

func TestResultContextOptIn(t *testing.T) {
	for _, withCases := range []bool{false, true} {
		req := contracts.RunRequest{Execution: &contracts.ExecutionSpec{Mode: "execution", Inputs: []string{"hello"}}, Metadata: map[string]any{"task_version_id": "55555555-5555-4555-8555-555555555555"}}
		result := ExecutionResult{Status: contracts.StatusInternalError}
		if withCases {
			result = ExecutionResult{Status: contracts.StatusOK, Cases: []ExecutionCaseResult{{Status: contracts.StatusOK}}}
		}
		payload, _ := json.Marshal(RunResultFromExecution(req, result, ResultTimingInput{}, "worker"))
		var wire map[string]any
		_ = json.Unmarshal(payload, &wire)
		if _, ok := wire["evaluation_context"]; ok {
			t.Fatal("legacy request received new field")
		}
		req.Metadata["result_context_version"] = "1"
		payload, _ = json.Marshal(RunResultFromExecution(req, result, ResultTimingInput{}, "worker"))
		_ = json.Unmarshal(payload, &wire)
		ctx, ok := wire["evaluation_context"].(map[string]any)
		if !ok || ctx["task_version_id"] != "55555555-5555-4555-8555-555555555555" || ctx["suite_sha256"] != "c7a0f7154e64cd96c617f251dc12c4396b7234c2856ccf4860ab7af537dfcdd9" {
			t.Fatalf("missing/wrong context: %s", payload)
		}
	}
}
