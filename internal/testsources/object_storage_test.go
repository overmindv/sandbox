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
	if sourceErr.Reason != "test_suite_checksum_mismatch" {
		t.Errorf("expected cached checksum reason, got %q", sourceErr.Reason)
	}
}

func TestObjectStorageProviderUsesCachePolicy(t *testing.T) {
	store := &countingStore{inner: objectstore.NewMemoryStore("tests")}
	suite := validSuite()
	payload, ok := putSuitePayload(t, store, "tests", "sandbox-tests", suite, suite.Version)
	if !ok {
		return
	}

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:    true,
		Provider:   "s3",
		Bucket:     "tests",
		KeyPrefix:  "sandbox-tests",
		CacheTTLMS: 60000,
		MaxBytes:   4096,
	}, store)
	provider.now = func() time.Time { return now }
	policy := provider.CachePolicy()
	if !policy.Enabled {
		t.Error("expected cache policy to be enabled")
	}
	if policy.TTL != time.Minute {
		t.Errorf("expected cache ttl 1m, got %s", policy.TTL)
	}

	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = suite.Version
	req.Tests.ChecksumSHA256 = SHA256Hex(payload)

	if _, err := provider.Resolve(context.Background(), req); err != nil {
		t.Errorf("first resolve: %v", err)
		return
	}
	if _, err := provider.Resolve(context.Background(), req); err != nil {
		t.Errorf("second resolve: %v", err)
		return
	}
	if store.getCalls != 1 {
		t.Errorf("expected one object storage fetch with warm cache, got %d", store.getCalls)
	}
}

func TestObjectStorageProviderRefreshesExpiredCache(t *testing.T) {
	store := &countingStore{inner: objectstore.NewMemoryStore("tests")}
	suite := validSuite()
	payload, ok := putSuitePayload(t, store, "tests", "sandbox-tests", suite, suite.Version)
	if !ok {
		return
	}

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:    true,
		Provider:   "s3",
		Bucket:     "tests",
		KeyPrefix:  "sandbox-tests",
		CacheTTLMS: 1000,
		MaxBytes:   4096,
	}, store)
	provider.now = func() time.Time { return now }

	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = suite.Version
	req.Tests.ChecksumSHA256 = SHA256Hex(payload)

	if _, err := provider.Resolve(context.Background(), req); err != nil {
		t.Errorf("first resolve: %v", err)
		return
	}
	now = now.Add(2 * time.Second)
	if _, err := provider.Resolve(context.Background(), req); err != nil {
		t.Errorf("resolve after cache expiry: %v", err)
		return
	}
	if store.getCalls != 2 {
		t.Errorf("expected cache expiry to fetch suite again, got %d fetches", store.getCalls)
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

func TestObjectStorageProviderRejectsOversizedManifest(t *testing.T) {
	store := objectstore.NewMemoryStore("tests")
	suite := validSuite()
	payload, ok := putSuitePayload(t, store, "tests", "sandbox-tests", suite, suite.Version)
	if !ok {
		return
	}

	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:   true,
		Provider:  "s3",
		Bucket:    "tests",
		KeyPrefix: "sandbox-tests",
		MaxBytes:  len(payload) - 1,
	}, store)
	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = suite.Version
	req.Tests.ChecksumSHA256 = SHA256Hex(payload)

	_, err := provider.Resolve(context.Background(), req)
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected SourceError, got %v", err)
		return
	}
	if sourceErr.Reason != "test_suite_too_large" {
		t.Errorf("expected oversized manifest reason, got %q", sourceErr.Reason)
	}
	if sourceErr.Retryable {
		t.Error("expected oversized manifest to be non-retryable")
	}
}

func TestObjectStorageProviderRejectsFileChecksumMismatch(t *testing.T) {
	store := objectstore.NewMemoryStore("tests")
	suite := validSuite()
	suite.Files[0].ChecksumSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	payload, ok := putSuitePayload(t, store, "tests", "sandbox-tests", suite, suite.Version)
	if !ok {
		return
	}

	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:   true,
		Provider:  "s3",
		Bucket:    "tests",
		KeyPrefix: "sandbox-tests",
		MaxBytes:  4096,
	}, store)
	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = suite.Version
	req.Tests.ChecksumSHA256 = SHA256Hex(payload)

	_, err := provider.Resolve(context.Background(), req)
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected SourceError, got %v", err)
		return
	}
	if sourceErr.Reason != "invalid_test_suite" {
		t.Errorf("expected invalid suite reason, got %q", sourceErr.Reason)
	}
	if sourceErr.Retryable {
		t.Error("expected file checksum mismatch to be non-retryable")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("expected error to wrap ErrChecksumMismatch, got %v", err)
	}
}

func TestObjectStorageProviderRejectsSuiteVersionMismatch(t *testing.T) {
	store := objectstore.NewMemoryStore("tests")
	suite := validSuite()
	requestedVersion := "v2"
	payload, ok := putSuitePayload(t, store, "tests", "sandbox-tests", suite, requestedVersion)
	if !ok {
		return
	}

	provider := NewObjectStorageProvider(config.ObjectStorageTestsConfig{
		Enabled:   true,
		Provider:  "s3",
		Bucket:    "tests",
		KeyPrefix: "sandbox-tests",
		MaxBytes:  4096,
	}, store)
	req := validObjectStorageRunRequest()
	req.Tests.SuiteID = suite.SuiteID
	req.Tests.Version = requestedVersion
	req.Tests.ChecksumSHA256 = SHA256Hex(payload)

	_, err := provider.Resolve(context.Background(), req)
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected SourceError, got %v", err)
		return
	}
	if sourceErr.Reason != "test_suite_version_mismatch" {
		t.Errorf("expected version mismatch reason, got %q", sourceErr.Reason)
	}
}

func TestValidateSuiteWithOptionsRejectsOversizedDecodedBundle(t *testing.T) {
	suite := validSuite()
	content, err := base64.StdEncoding.DecodeString(suite.Files[0].ContentB64)
	if err != nil {
		t.Errorf("decode fixture: %v", err)
		return
	}

	err = ValidateSuiteWithOptions(suite, SuiteValidationOptions{MaxDecodedBytes: len(content) - 1})
	if !errors.Is(err, ErrInvalidSuite) {
		t.Errorf("expected invalid suite for oversized decoded bundle, got %v", err)
	}
	if !errors.Is(err, ErrSuiteTooLarge) {
		t.Errorf("expected oversized decoded bundle to wrap ErrSuiteTooLarge, got %v", err)
	}
}

func TestSuiteKeyUsesLatestAndRejectsUnsafeSegments(t *testing.T) {
	key, err := SuiteKey("sandbox-tests/", "suite-1", "")
	if err != nil {
		t.Errorf("build latest suite key: %v", err)
		return
	}
	if key != "sandbox-tests/test-suites/suite-1/latest.json" {
		t.Errorf("expected latest suite key, got %q", key)
	}

	if _, err := SuiteKey("sandbox-tests", "../suite", "v1"); !errors.Is(err, ErrInvalidSuite) {
		t.Errorf("expected unsafe suite id to be rejected, got %v", err)
	}
	if _, err := SuiteKey("sandbox-tests", "suite-1", "../v1"); !errors.Is(err, ErrInvalidSuite) {
		t.Errorf("expected unsafe version to be rejected, got %v", err)
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

type countingStore struct {
	inner    objectstore.Store
	getCalls int
}

func (s *countingStore) Provider() string {
	return s.inner.Provider()
}

func (s *countingStore) Put(ctx context.Context, object objectstore.Object) (objectstore.StoredObject, error) {
	return s.inner.Put(ctx, object)
}

func (s *countingStore) Get(ctx context.Context, bucket string, key string) ([]byte, error) {
	s.getCalls++
	return s.inner.Get(ctx, bucket, key)
}

func (s *countingStore) Close() error {
	return s.inner.Close()
}

func putSuitePayload(t *testing.T, store objectstore.Store, bucket string, prefix string, suite Suite, version string) ([]byte, bool) {
	payload, err := json.Marshal(suite)
	if err != nil {
		t.Errorf("marshal suite: %v", err)
		return nil, false
	}
	key, err := SuiteKey(prefix, suite.SuiteID, version)
	if err != nil {
		t.Errorf("build suite key: %v", err)
		return nil, false
	}
	if _, err := store.Put(context.Background(), objectstore.Object{
		Bucket:      bucket,
		Key:         key,
		ContentType: "application/json",
		Content:     payload,
	}); err != nil {
		t.Errorf("put suite: %v", err)
		return nil, false
	}
	return payload, true
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
