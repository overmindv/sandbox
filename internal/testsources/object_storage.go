package testsources

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
)

const SuiteSchemaVersion = "sandbox.testsuite.v1"

type CachePolicy struct {
	Enabled bool
	TTL     time.Duration
}

type SuiteValidationOptions struct {
	MaxDecodedBytes int
}

type Suite struct {
	SchemaVersion string                 `json:"schema_version"`
	SuiteID       string                 `json:"suite_id"`
	Version       string                 `json:"version,omitempty"`
	Mode          contracts.TestMode     `json:"mode"`
	Entrypoint    string                 `json:"entrypoint,omitempty"`
	Files         []contracts.SourceFile `json:"files"`
	Metadata      map[string]any         `json:"metadata,omitempty"`
}

type ObjectStorageProvider struct {
	cfg   config.ObjectStorageTestsConfig
	store objectstore.Reader
	mu    sync.Mutex
	cache map[string]cachedSuite
	now   func() time.Time
}

type cachedSuite struct {
	suite         Suite
	payloadSHA256 string
	expiresAt     time.Time
}

func NewObjectStorageProvider(cfg config.ObjectStorageTestsConfig, store objectstore.Reader) *ObjectStorageProvider {
	return &ObjectStorageProvider{
		cfg:   cfg,
		store: store,
		cache: make(map[string]cachedSuite),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func CachePolicyFromConfig(cfg config.ObjectStorageTestsConfig) CachePolicy {
	ttl := time.Duration(cfg.CacheTTLMS) * time.Millisecond
	return CachePolicy{
		Enabled: ttl > 0,
		TTL:     ttl,
	}
}

func (p *ObjectStorageProvider) CachePolicy() CachePolicy {
	if p == nil {
		return CachePolicy{}
	}
	return CachePolicyFromConfig(p.cfg)
}

func (p *ObjectStorageProvider) Resolve(ctx context.Context, req contracts.RunRequest) (contracts.RunRequest, error) {
	if err := ctx.Err(); err != nil {
		return contracts.RunRequest{}, err
	}
	if p == nil || p.store == nil {
		return contracts.RunRequest{}, SourceError{
			Reason:    "object_storage_tests_disabled",
			Retryable: false,
			Err:       ErrObjectStorageTestsDisabled,
		}
	}

	key, err := SuiteKey(p.cfg.KeyPrefix, req.Tests.SuiteID, req.Tests.Version)
	if err != nil {
		return contracts.RunRequest{}, SourceError{Reason: "invalid_test_suite_ref", Retryable: false, Err: err}
	}

	if suite, ok, err := p.getCached(key, req.Tests.ChecksumSHA256); err != nil {
		return contracts.RunRequest{}, err
	} else if ok {
		return applySuite(req, suite)
	}

	payload, err := p.store.Get(ctx, p.cfg.Bucket, key)
	if err != nil {
		if errors.Is(err, objectstore.ErrTooLarge) {
			return contracts.RunRequest{}, SourceError{Reason: "test_suite_too_large", Err: ErrSuiteTooLarge}
		}
		return contracts.RunRequest{}, SourceError{Reason: "test_suite_fetch_failed", Retryable: true, Err: err}
	}
	if len(payload) > suiteByteLimit(p.cfg.MaxBytes) {
		return contracts.RunRequest{}, SourceError{
			Reason:    "test_suite_too_large",
			Retryable: false,
			Err:       fmt.Errorf("%w: %d bytes exceeds %d", ErrInvalidSuite, len(payload), p.cfg.MaxBytes),
		}
	}
	payloadSHA256 := SHA256Hex(payload)
	if err := VerifySHA256(payload, req.Tests.ChecksumSHA256); err != nil {
		return contracts.RunRequest{}, SourceError{Reason: "test_suite_checksum_mismatch", Retryable: false, Err: err}
	}

	var suite Suite
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		return contracts.RunRequest{}, SourceError{Reason: "test_suite_decode_failed", Retryable: false, Err: err}
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return contracts.RunRequest{}, SourceError{Reason: "test_suite_decode_failed", Err: ErrInvalidSuite}
	}
	if err := ValidateSuiteWithOptions(suite, SuiteValidationOptions{MaxDecodedBytes: suiteByteLimit(p.cfg.MaxBytes)}); err != nil {
		reason := "invalid_test_suite"
		if errors.Is(err, ErrSuiteTooLarge) {
			reason = "test_suite_too_large"
		}
		return contracts.RunRequest{}, SourceError{Reason: reason, Retryable: false, Err: err}
	}
	if suite.SuiteID != req.Tests.SuiteID {
		return contracts.RunRequest{}, SourceError{
			Reason:    "test_suite_id_mismatch",
			Retryable: false,
			Err:       fmt.Errorf("%w: requested %q got %q", ErrInvalidSuite, req.Tests.SuiteID, suite.SuiteID),
		}
	}
	if req.Tests.Version != "" && suite.Version != "" && suite.Version != req.Tests.Version {
		return contracts.RunRequest{}, SourceError{
			Reason:    "test_suite_version_mismatch",
			Retryable: false,
			Err:       fmt.Errorf("%w: requested %q got %q", ErrInvalidSuite, req.Tests.Version, suite.Version),
		}
	}
	if req.Tests.Mode != "" && suite.Mode != req.Tests.Mode {
		return contracts.RunRequest{}, SourceError{
			Reason:    "test_suite_mode_mismatch",
			Retryable: false,
			Err:       fmt.Errorf("%w: requested %q got %q", ErrInvalidSuite, req.Tests.Mode, suite.Mode),
		}
	}

	p.putCached(key, suite, payloadSHA256)
	return applySuite(req, suite)
}

func ValidateSuite(suite Suite) error {
	return ValidateSuiteWithOptions(suite, SuiteValidationOptions{})
}

func ValidateSuiteWithOptions(suite Suite, opts SuiteValidationOptions) error {
	if suite.SchemaVersion != SuiteSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %q", ErrInvalidSuite, SuiteSchemaVersion)
	}
	if strings.TrimSpace(suite.SuiteID) == "" {
		return fmt.Errorf("%w: suite_id is required", ErrInvalidSuite)
	}
	if suite.Mode == "" {
		return fmt.Errorf("%w: mode is required", ErrInvalidSuite)
	}
	if len(suite.Files) == 0 {
		return fmt.Errorf("%w: at least one file is required", ErrInvalidSuite)
	}
	if len(suite.Files) > 32 {
		return ErrSuiteTooLarge
	}
	seen := map[string]bool{}
	totalDecodedBytes := 0
	for _, file := range suite.Files {
		if strings.TrimSpace(file.Path) == "" {
			return fmt.Errorf("%w: file path is required", ErrInvalidSuite)
		}
		normalized, err := objectstore.NormalizeKey(file.Path)
		if err != nil || normalized != file.Path || len(file.Path) > 128 || seen[file.Path] {
			return ErrInvalidSuite
		}
		seen[file.Path] = true
		if base64.StdEncoding.DecodedLen(len(file.ContentB64)) > 1048578 {
			return ErrSuiteTooLarge
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentB64)
		if err != nil {
			return fmt.Errorf("%w: decode file %q: %w", ErrInvalidSuite, file.Path, err)
		}
		totalDecodedBytes += len(content)
		if len(content) > 1048576 || totalDecodedBytes > suiteByteLimit(opts.MaxDecodedBytes) {
			return fmt.Errorf("%w: %w: decoded test bundle is %d bytes, max is %d", ErrInvalidSuite, ErrSuiteTooLarge, totalDecodedBytes, opts.MaxDecodedBytes)
		}
		if err := VerifySHA256(content, file.ChecksumSHA256); err != nil {
			return fmt.Errorf("%w: file %q: %w", ErrInvalidSuite, file.Path, err)
		}
	}
	return nil
}

func SuiteKey(prefix string, suiteID string, version string) (string, error) {
	suiteID = strings.TrimSpace(suiteID)
	if suiteID == "" {
		return "", fmt.Errorf("%w: suite_id is required", ErrInvalidSuite)
	}
	if !safeSegment(suiteID) {
		return "", fmt.Errorf("%w: unsafe suite_id %q", ErrInvalidSuite, suiteID)
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = "latest"
	}
	if !safeSegment(version) {
		return "", fmt.Errorf("%w: unsafe version %q", ErrInvalidSuite, version)
	}
	key := path.Join(strings.Trim(prefix, "/"), "test-suites", suiteID, version+".json")
	return objectstore.NormalizeKey(key)
}

func applySuite(req contracts.RunRequest, suite Suite) (contracts.RunRequest, error) {
	req.Tests.Source = contracts.TestSourceInline
	req.Tests.Mode = suite.Mode
	req.Tests.Entrypoint = suite.Entrypoint
	req.Tests.Files = cloneSourceFiles(suite.Files)
	req.Tests.SuiteID = ""
	req.Tests.Version = ""
	req.Tests.ChecksumSHA256 = ""
	return req, nil
}

func (p *ObjectStorageProvider) getCached(key string, expectedChecksum string) (Suite, bool, error) {
	policy := p.CachePolicy()
	if !policy.Enabled {
		return Suite{}, false, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	item, ok := p.cache[key]
	if !ok || !item.expiresAt.After(p.now()) {
		if ok {
			delete(p.cache, key)
		}
		return Suite{}, false, nil
	}
	expectedChecksum = strings.ToLower(strings.TrimSpace(expectedChecksum))
	if expectedChecksum != "" && item.payloadSHA256 != expectedChecksum {
		return Suite{}, false, SourceError{
			Reason:    "test_suite_checksum_mismatch",
			Retryable: false,
			Err:       fmt.Errorf("%w: expected %s got %s", ErrChecksumMismatch, expectedChecksum, item.payloadSHA256),
		}
	}
	return cloneSuite(item.suite), true, nil
}

func (p *ObjectStorageProvider) putCached(key string, suite Suite, payloadSHA256 string) {
	policy := p.CachePolicy()
	if !policy.Enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = cachedSuite{
		suite:         cloneSuite(suite),
		payloadSHA256: payloadSHA256,
		expiresAt:     p.now().Add(policy.TTL),
	}
}

func cloneSuite(suite Suite) Suite {
	suite.Files = cloneSourceFiles(suite.Files)
	if len(suite.Metadata) > 0 {
		metadata := make(map[string]any, len(suite.Metadata))
		for key, value := range suite.Metadata {
			metadata[key] = value
		}
		suite.Metadata = metadata
	}
	return suite
}

func cloneSourceFiles(files []contracts.SourceFile) []contracts.SourceFile {
	return append([]contracts.SourceFile(nil), files...)
}

func safeSegment(value string) bool {
	if value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// suiteByteLimit задаёт жёсткий предел независимо от локального config.
func suiteByteLimit(configured int) int {
	const hardMax = 8 * 1024 * 1024
	if configured <= 0 || configured > hardMax {
		return hardMax
	}
	return configured
}
