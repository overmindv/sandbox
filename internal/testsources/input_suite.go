package testsources

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
	"regexp"
	"strings"
)

// ResolveInputs загружает только stdin; эталоны и checker остаются в tasks.
func (p *ObjectStorageProvider) ResolveInputs(ctx context.Context, req contracts.RunRequest) (contracts.RunRequest, error) {
	fail := func(reason string, retry bool) (contracts.RunRequest, error) {
		return contracts.RunRequest{}, SourceError{Reason: reason, Retryable: retry, Err: ErrInvalidSuite}
	}
	if p == nil || p.store == nil {
		return fail("object_storage_tests_disabled", false)
	}
	if req.Execution == nil || req.Execution.Suite == nil {
		return fail("invalid_input_suite_ref", false)
	}
	ref := req.Execution.Suite
	if !regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`).MatchString(ref.SuiteID) || ref.Version == "" || strings.EqualFold(ref.Version, "latest") || len(ref.Version) > 64 || !safeSegment(ref.Version) || len(ref.ChecksumSHA256) != 64 {
		return fail("invalid_input_suite_ref", false)
	}
	if _, err := hex.DecodeString(ref.ChecksumSHA256); err != nil {
		return fail("invalid_input_suite_ref", false)
	}
	key := ref.SuiteID + "/" + ref.Version + "/suite.json"
	if p.cfg.KeyPrefix != "" {
		key = p.cfg.KeyPrefix + "/" + key
	}
	payload, err := p.store.Get(ctx, p.cfg.Bucket, key)
	if err != nil {
		if errors.Is(err, objectstore.ErrTooLarge) {
			return fail("input_suite_too_large", false)
		}
		return fail("input_suite_fetch_failed", true)
	}
	maxBytes := suiteByteLimit(p.cfg.MaxBytes)
	if len(payload) > maxBytes {
		return fail("input_suite_too_large", false)
	}
	if VerifySHA256(payload, ref.ChecksumSHA256) != nil {
		return fail("input_suite_checksum_mismatch", false)
	}
	var manifest struct {
		SchemaVersion string   `json:"schema_version"`
		SuiteID       string   `json:"suite_id"`
		Version       string   `json:"version"`
		Inputs        []string `json:"inputs"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return fail("invalid_input_suite", false)
	}
	if manifest.SchemaVersion != "sandbox.inputs.v1" || manifest.SuiteID != ref.SuiteID || manifest.Version != ref.Version || len(manifest.Inputs) == 0 || len(manifest.Inputs) > 20 {
		return fail("invalid_input_suite", false)
	}
	total := 0
	for _, input := range manifest.Inputs {
		total += len(input)
		if len(input) > 65536 {
			return fail("input_suite_too_large", false)
		}
	}
	if total > 1048576 {
		return fail("input_suite_too_large", false)
	}
	req.Execution = &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: manifest.Inputs}
	return req, nil
}
