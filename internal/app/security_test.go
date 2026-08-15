package app

import (
	"testing"

	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/security"
)

func TestApplyOutputPolicyTruncatesExecutionLogs(t *testing.T) {
	policy := security.DefaultPolicy()
	policy.MaxStdoutBytes = 4
	policy.MaxStderrBytes = 3

	result := applyOutputPolicy(execution.ExecutionResult{
		Stdout: "abcdef",
		Stderr: "vwxyz",
	}, policy)

	if result.Stdout != "abcd" || result.Stderr != "vwx" {
		t.Errorf("unexpected truncated logs stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
	if !result.LogsTruncated {
		t.Error("expected logs truncated marker")
	}
}
