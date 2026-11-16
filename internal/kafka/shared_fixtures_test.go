package kafka

import (
	"os"
	"path/filepath"
	"testing"

	"overmindv/sandbox/internal/contracts"
)

// Те же fixtures проходят schemas обеих сторон и decoder/validator sandbox.
func TestSharedExecutionFixtures(t *testing.T) {
	t.Parallel()
	root := "../../tests/fixtures/execution-contract"
	for _, mode := range []string{"execution", "execution-go", "pytest", "go-test"} {
		t.Run(mode, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(root, mode+".request.json"))
			if err != nil {
				t.Fatal(err)
			}
			request, err := DecodeRunRequest(payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := contracts.ValidateRunRequest(request); err != nil {
				t.Fatal(err)
			}
			if request.Metadata["task_version_id"] != "55555555-5555-4555-8555-555555555555" {
				t.Fatal("task version lost")
			}
			payload, err = os.ReadFile(filepath.Join(root, mode+".result.json"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := DecodeRunResult(payload)
			if err != nil {
				t.Fatal(err)
			}
			if result.SubmissionID != request.SubmissionID || result.AttemptID != request.AttemptID {
				t.Fatal("result identity mismatch")
			}
		})
	}
}
