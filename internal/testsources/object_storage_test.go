package testsources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
)

func TestObjectStorageProviderResolvesSuite(t *testing.T) {
	store := objectstore.NewMemoryStore("tests")
	suite := validSuite()
	payload, err := json.Marshal(suite)
	if err != nil {
		t.Errorf("marshal suite: %v", err)
		return
	}
	key, err := SuiteKey("sandbox-tests", suite.SuiteID, suite.Version)
	if err != nil {
		t.Errorf("build suite key: %v", err)
		return
	}
	if _, err := store.Put(context.Background(), objectstore.Object{
		Bucket:      "tests",
		Key:         key,
		ContentType: "application/json",
		Content:     payload,
	}); err != nil {
		t.Errorf("put suite: %v", err)
		return
	}

	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:    true,
		Provider:   "s3",
		Bucket:     "tests",
		KeyPrefix:  "sandbox-tests",
		CacheTTLMS: 1000,
		MaxBytes:   1024,
	}, store)
	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = suite.Version
	req.Tests.ChecksumSHA256 = SHA256Hex(payload)

	resolved, err := provider.Resolve(context.Background(), req)
	if err != nil {
		t.Errorf("resolve suite: %v", err)
		return
	}
	if resolved.Tests.Source != contracts.TestSourceInline {
		t.Errorf("expected inline source after resolution, got %q", resolved.Tests.Source)
	}
	if resolved.Tests.SuiteID != "" || resolved.Tests.Version != "" || resolved.Tests.ChecksumSHA256 != "" {
		t.Errorf("expected object storage reference fields to be cleared, got %#v", resolved.Tests)
	}
	if resolved.Tests.Entrypoint != suite.Entrypoint {
		t.Errorf("expected entrypoint %q, got %q", suite.Entrypoint, resolved.Tests.Entrypoint)
	}
	if len(resolved.Tests.Files) != 1 {
		t.Errorf("expected one resolved file, got %d", len(resolved.Tests.Files))
		return
	}
	if resolved.Tests.Files[0].Path != "test_solution.py" {
		t.Errorf("expected resolved test path, got %q", resolved.Tests.Files[0].Path)
	}

	req.Tests.ChecksumSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = provider.Resolve(context.Background(), req)
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected cached checksum SourceError, got %v", err)
		return
	}
	if sourceErr.Retryable {
		t.Error("expected cached checksum mismatch to be non-retryable")
	}
}

func TestObjectStorageProviderRejectsChecksumMismatch(t *testing.T) {
	store := objectstore.NewMemoryStore("tests")
	suite := validSuite()
	payload, err := json.Marshal(suite)
	if err != nil {
		t.Errorf("marshal suite: %v", err)
		return
	}
	key, err := SuiteKey("sandbox-tests", suite.SuiteID, suite.Version)
	if err != nil {
		t.Errorf("build suite key: %v", err)
		return
	}
	if _, err := store.Put(context.Background(), objectstore.Object{Bucket: "tests", Key: key, Content: payload}); err != nil {
		t.Errorf("put suite: %v", err)
		return
	}

	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:   true,
		Provider:  "s3",
		Bucket:    "tests",
		KeyPrefix: "sandbox-tests",
		MaxBytes:  1024,
	}, store)
	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = suite.Version
	req.Tests.ChecksumSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	_, err = provider.Resolve(context.Background(), req)
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected SourceError, got %v", err)
		return
	}
	if sourceErr.Retryable {
		t.Error("expected checksum mismatch to be non-retryable")
	}
	if sourceErr.Reason != "test_suite_checksum_mismatch" {
		t.Errorf("expected checksum reason, got %q", sourceErr.Reason)
	}
}

func TestResolverRejectsObjectStorageWhenDisabled(t *testing.T) {
	provider, err := NewProviderFromConfig(config.TestsConfig{})
	if err != nil {
		t.Errorf("create provider: %v", err)
		return
	}

	_, err = provider.Resolve(context.Background(), validObjectStorageRunRequest())
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected SourceError, got %v", err)
		return
	}
	if sourceErr.Retryable {
		t.Error("expected disabled object storage tests to be non-retryable")
	}
}

func validSuite() Suite {
	content := []byte("from solution import add\n\ndef test_add():\n    assert add(1, 2) == 3\n")
	return Suite{
		SchemaVersion: SuiteSchemaVersion,
		SuiteID:       "suite-1",
		Version:       "v1",
		Mode:          contracts.TestModePytest,
		Entrypoint:    "test_solution.py",
		Files: []contracts.SourceFile{
			{
				Path:           "test_solution.py",
				ContentB64:     base64.StdEncoding.EncodeToString(content),
				ChecksumSHA256: SHA256Hex(content),
			},
		},
	}
}

func validObjectStorageRunRequest() contracts.RunRequest {
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
				{
					Path:       "solution.py",
					ContentB64: base64.StdEncoding.EncodeToString([]byte("def add(a, b):\n    return a + b\n")),
				},
			},
		},
		Tests: contracts.TestSpec{
			Mode:    contracts.TestModePytest,
			Source:  contracts.TestSourceObjectStorage,
			SuiteID: "suite-1",
			Version: "v1",
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
		TraceID:   "trace-test-source",
		CreatedAt: time.Now().UTC(),
	}
}
