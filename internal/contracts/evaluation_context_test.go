package contracts

import "testing"

func TestEvaluationContextOmitsInvalidVersion(t *testing.T) {
	for _, version := range []any{"", "invalid", 42, []any{"bad"}} {
		req := validRunRequest()
		req.Execution = &ExecutionSpec{Mode: ExecutionMode, Inputs: []string{"x"}}
		req.Metadata = map[string]any{"result_context_version": "1", "task_version_id": version}
		if got := EvaluationContextForRequest(req); got != nil {
			t.Fatalf("invalid version generated context: %+v", got)
		}
	}
}
