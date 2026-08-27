package security

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/contracts"
)

func TestNormalizeRelativePathRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		code string
	}{
		{name: "parent segment", path: "../solution.py", code: CodePathTraversal},
		{name: "absolute posix", path: "/workspace/solution.py", code: CodeAbsolutePath},
		{name: "windows drive", path: "C:/tmp/solution.py", code: CodeAbsolutePath},
		{name: "backslash", path: `dir\solution.py`, code: CodeUnsafeFilename},
		{name: "empty segment", path: "dir//solution.py", code: CodeUnsafeFilename},
		{name: "current segment", path: "dir/./solution.py", code: CodeUnsafeFilename},
		{name: "shell metacharacter", path: "solution.py;rm", code: CodeUnsafeFilename},
		{name: "percent encoded traversal", path: "%2e%2e/solution.py", code: CodeUnsafeFilename},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeRelativePath(tt.path, DefaultPolicy())
			if !hasViolationCode(err, tt.code) {
				t.Errorf("expected %s for %q, got %v", tt.code, tt.path, err)
			}
		})
	}
}

func TestNormalizeRelativePathAcceptsSafePath(t *testing.T) {
	normalized, err := NormalizeRelativePath("src/tasks/solution_test.py", DefaultPolicy())
	if err != nil {
		t.Errorf("normalize safe path: %v", err)
	}
	if normalized != "src/tasks/solution_test.py" {
		t.Errorf("expected normalized path unchanged, got %q", normalized)
	}
}

func TestGuardRejectsFileAndPayloadLimits(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		mutate func(*contracts.RunRequest)
		code   string
	}{
		{
			name:   "too many files",
			policy: policyWith(func(p *Policy) { p.MaxFiles = 1 }),
			mutate: func(req *contracts.RunRequest) {},
			code:   CodeTooManyFiles,
		},
		{
			name:   "single file too large",
			policy: policyWith(func(p *Policy) { p.MaxFileBytes = 4 }),
			mutate: func(req *contracts.RunRequest) {},
			code:   CodeFileTooLarge,
		},
		{
			name:   "decoded payload too large",
			policy: policyWith(func(p *Policy) { p.MaxPayloadBytes = 8 }),
			mutate: func(req *contracts.RunRequest) {},
			code:   CodePayloadTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validSecurityRunRequest()
			tt.mutate(&req)
			err := NewGuard(tt.policy).ValidateRunRequest(req)
			if !hasViolationCode(err, tt.code) {
				t.Errorf("expected %s, got %v", tt.code, err)
			}
		})
	}
}

func TestGuardRejectsOversizedRawPayloadBeforeDecode(t *testing.T) {
	policy := policyWith(func(p *Policy) { p.MaxPayloadBytes = 4 })
	err := NewGuard(policy).ValidatePayload([]byte("12345"))

	if !hasViolationCode(err, CodePayloadTooLarge) {
		t.Errorf("expected payload limit error, got %v", err)
	}
}

func TestArchiveGuardRejectsExtensionAndMagic(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content []byte
	}{
		{name: "zip extension", path: "solution.zip", content: []byte("plain")},
		{name: "zip magic", path: "solution.py", content: []byte{'P', 'K', 0x03, 0x04, 'x'}},
		{name: "tar magic", path: "solution.py", content: tarHeader()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateArchiveDisabled(tt.path, tt.content, DefaultPolicy())
			if !hasViolationCode(err, CodeArchiveDisabled) {
				t.Errorf("expected archive disabled, got %v", err)
			}
		})
	}
}

func TestCopyOutSymlinkPolicyRejectsSymlinkMode(t *testing.T) {
	if _, err := ValidateCopyOutPath(".sandbox/pytest-report.json", DefaultPolicy()); err != nil {
		t.Errorf("expected copy-out path to be syntactically valid: %v", err)
	}

	err := RejectSymlinkCopyOut(".sandbox/pytest-report.json", os.ModeSymlink, DefaultPolicy())
	if !hasViolationCode(err, CodeCopyOutSymlink) {
		t.Errorf("expected copy-out symlink rejection, got %v", err)
	}
}

func TestTruncateOutputPolicy(t *testing.T) {
	policy := policyWith(func(p *Policy) {
		p.MaxStdoutBytes = 5
		p.MaxStderrBytes = 3
	})

	stdout, stderr, truncated := TruncateOutput("hello world", "abcdef", policy)
	if stdout != "hello" || stderr != "abc" || !truncated {
		t.Errorf("unexpected truncation result stdout=%q stderr=%q truncated=%v", stdout, stderr, truncated)
	}
}

func TestRuntimeEnvironmentRejectsSensitiveNames(t *testing.T) {
	tests := []string{
		"AWS_ACCESS_KEY_ID",
		"SANDBOX_SECRET_ACCESS_KEY",
		"DATABASE_PASSWORD",
		"PRIVATE_KEY",
		"API_TOKEN",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			err := ValidateRuntimeEnvironment(map[string]string{name: "secret"})
			if !hasViolationCode(err, CodeRuntimeSecretEnv) {
				t.Errorf("expected runtime secret env violation, got %v", err)
			}
		})
	}
}

func TestRuntimeEnvironmentAllowsNonSensitiveRuntimeNames(t *testing.T) {
	err := ValidateRuntimeEnvironment(map[string]string{
		"PATH":                           "/usr/local/bin:/usr/bin:/bin",
		"PYTHONDONTWRITEBYTECODE":        "1",
		"PYTHONHASHSEED":                 "0",
		"SANDBOX_PYTEST_REPORT":          ".sandbox/pytest-report.json",
		"PYTEST_DISABLE_PLUGIN_AUTOLOAD": "1",
	})
	if err != nil {
		t.Errorf("expected runtime environment to pass, got %v", err)
	}
}

func validSecurityRunRequest() contracts.RunRequest {
	return contracts.RunRequest{
		SchemaVersion: contracts.RunRequestSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		UserID:        "33333333-3333-4333-8333-333333333333",
		TaskID:        "44444444-4444-4444-8444-444444444444",
		Language: contracts.LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Code: contracts.CodeSpec{
			Entrypoint: "solution.py",
			Files: []contracts.SourceFile{
				{Path: "solution.py", ContentB64: encodeSecurityFixture("def add(a, b):\n    return a + b\n")},
			},
		},
		Tests: contracts.TestSpec{
			Mode:       contracts.TestModePytest,
			Source:     contracts.TestSourceInline,
			Entrypoint: "test_solution.py",
			Files: []contracts.SourceFile{
				{Path: "test_solution.py", ContentB64: encodeSecurityFixture("from solution import add\n")},
			},
		},
		Limits: contracts.Limits{
			CPUms:          2000,
			Wallms:         5000,
			MemoryMB:       128,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
		TraceID:   "trace-security",
		CreatedAt: time.Now().UTC(),
	}
}

func encodeSecurityFixture(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func policyWith(update func(*Policy)) Policy {
	policy := DefaultPolicy()
	update(&policy)
	return policy
}

func hasViolationCode(err error, code string) bool {
	var violationErr ViolationError
	return errors.As(err, &violationErr) && violationErr.Code == code
}

func tarHeader() []byte {
	content := []byte(strings.Repeat("\x00", 512))
	copy(content[257:262], "ustar")
	return content
}
