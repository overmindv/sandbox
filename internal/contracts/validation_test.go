package contracts

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestValidateRunRequestAcceptsValidInlinePython(t *testing.T) {
	req := validRunRequest()

	if err := ValidateRunRequest(req); err != nil {
		t.Errorf("expected request to be valid: %v", err)
	}
}

func TestValidateRunRequestRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "parent segment", path: "../solution.py"},
		{name: "absolute path", path: "/workspace/solution.py"},
		{name: "windows drive", path: "C:/tmp/solution.py"},
		{name: "backslash", path: `dir\solution.py`},
		{name: "empty segment", path: "dir//solution.py"},
		{name: "shell metacharacter", path: "solution.py;rm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRunRequest()
			req.Code.Files[0].Path = tt.path

			err := ValidateRunRequest(req)
			if err == nil {
				t.Errorf("expected %q to be rejected", tt.path)
			}
		})
	}
}

func TestValidateRunRequestRejectsInvalidBase64(t *testing.T) {
	req := validRunRequest()
	req.Code.Files[0].ContentB64 = "not base64"

	err := ValidateRunRequest(req)
	if err == nil || !strings.Contains(err.Error(), "content_b64") {
		t.Errorf("expected content_b64 validation error, got: %v", err)
	}
}

func TestValidateRunRequestRejectsDuplicateFiles(t *testing.T) {
	req := validRunRequest()
	req.Code.Files = append(req.Code.Files, req.Code.Files[0])

	err := ValidateRunRequest(req)
	if err == nil || !strings.Contains(err.Error(), "duplicate file path") {
		t.Errorf("expected duplicate file validation error, got: %v", err)
	}
}

func TestValidateRunRequestRejectsLimitsOverHardMax(t *testing.T) {
	req := validRunRequest()
	req.Limits.MemoryMB = 2048

	err := ValidateRunRequest(req)
	if err == nil || !strings.Contains(err.Error(), "limits.memory_mb") {
		t.Errorf("expected memory limit validation error, got: %v", err)
	}
}

func TestValidateRunRequestRejectsObjectStorageWithoutChecksum(t *testing.T) {
	req := validRunRequest()
	req.Tests = TestSpec{
		Mode:    TestModePytest,
		Source:  TestSourceObjectStorage,
		SuiteID: "python/addition",
	}

	err := ValidateRunRequest(req)
	if err == nil || !strings.Contains(err.Error(), "tests.checksum_sha256") {
		t.Errorf("expected checksum validation error, got: %v", err)
	}
}

func TestValidateRunRequestAcceptsObjectStorageTestsRef(t *testing.T) {
	req := validRunRequest()
	req.Tests = TestSpec{
		Mode:           TestModePytest,
		Source:         TestSourceObjectStorage,
		SuiteID:        "python-addition",
		Version:        "v1",
		ChecksumSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	if err := ValidateRunRequest(req); err != nil {
		t.Errorf("expected object storage tests ref to be valid: %v", err)
	}
}

func TestValidateRunRequestRejectsUnsafeObjectStorageSuiteID(t *testing.T) {
	req := validRunRequest()
	req.Tests = TestSpec{
		Mode:           TestModePytest,
		Source:         TestSourceObjectStorage,
		SuiteID:        "../python-addition",
		ChecksumSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	err := ValidateRunRequest(req)
	if err == nil || !strings.Contains(err.Error(), "tests.suite_id") {
		t.Errorf("expected suite id validation error, got: %v", err)
	}
}

func TestExecutionStatusValidityAndTerminalState(t *testing.T) {
	if !StatusPassed.Valid() || !StatusPassed.Terminal() {
		t.Error("passed must be valid and terminal")
	}
	if !StatusRunning.Valid() || StatusRunning.Terminal() {
		t.Error("running must be valid and non-terminal")
	}
	if ExecutionStatus("unknown").Valid() {
		t.Error("unknown status must be invalid")
	}
}

func validRunRequest() RunRequest {
	return RunRequest{
		SchemaVersion: RunRequestSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		UserID:        "33333333-3333-4333-8333-333333333333",
		TaskID:        "44444444-4444-4444-8444-444444444444",
		Language: LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Code: CodeSpec{
			Entrypoint: "solution.py",
			Files: []SourceFile{
				{
					Path:       "solution.py",
					ContentB64: encode("def add(a, b):\n    return a + b\n"),
				},
			},
		},
		Tests: TestSpec{
			Mode:       TestModePytest,
			Source:     TestSourceInline,
			Entrypoint: "test_solution.py",
			Files: []SourceFile{
				{
					Path:       "test_solution.py",
					ContentB64: encode("from solution import add\n\ndef test_add():\n    assert add(1, 2) == 3\n"),
				},
			},
		},
		Limits: Limits{
			CPUms:          2000,
			Wallms:         5000,
			MemoryMB:       128,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
		TraceID:   "trace-111",
		CreatedAt: time.Date(2026, 8, 12, 19, 0, 0, 0, time.UTC),
	}
}

func encode(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}
