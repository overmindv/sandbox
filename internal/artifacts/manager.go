package artifacts

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
	"overmindv/sandbox/internal/storage"
)

const textLogContentType = "text/plain; charset=utf-8"

type Manager struct {
	cfg   config.ArtifactStorageConfig
	store objectstore.Store
	now   func() time.Time
}

func NewManager(cfg config.ArtifactStorageConfig, store objectstore.Store) (*Manager, error) {
	if !cfg.Enabled {
		return &Manager{cfg: cfg, store: objectstore.DisabledStore{}, now: utcNow}, nil
	}
	if store == nil {
		return nil, fmt.Errorf("artifact object store is required when artifacts are enabled")
	}
	if cfg.InlineMaxBytes < 0 {
		return nil, fmt.Errorf("artifact inline max bytes must not be negative")
	}
	if cfg.MaxBytes < 0 {
		return nil, fmt.Errorf("artifact max bytes must not be negative")
	}
	if cfg.MaxBytes > 0 && cfg.InlineMaxBytes > cfg.MaxBytes {
		return nil, fmt.Errorf("artifact inline max bytes must not exceed artifact max bytes")
	}
	return &Manager{cfg: cfg, store: store, now: utcNow}, nil
}

func NewManagerFromConfig(cfg config.ArtifactStorageConfig) (*Manager, error) {
	store, err := objectstore.New(cfg)
	if err != nil {
		return nil, err
	}
	return NewManager(cfg, store)
}

func (m *Manager) ProcessResult(ctx context.Context, runID string, result contracts.RunResult) (contracts.RunResult, error) {
	result = contracts.RedactDiagnostics(result)
	if result.DiagnosticsVisibility != "public" {
		return result, nil
	}
	if m == nil || !m.cfg.Enabled {
		return result, nil
	}
	if runID == "" {
		return contracts.RunResult{}, fmt.Errorf("run id is required to store artifacts")
	}
	if result.AttemptID == "" {
		return contracts.RunResult{}, fmt.Errorf("attempt id is required to store artifacts")
	}

	originalLogsTruncated := result.Logs.Truncated
	stdout, err := m.processLog(ctx, runID, result, storage.ArtifactKindStdout, result.Logs.Stdout, originalLogsTruncated)
	if err != nil {
		return contracts.RunResult{}, err
	}
	if stdout.ref != nil {
		result.Logs.Stdout = stdout.inline
		result.Logs.StdoutArtifact = stdout.ref
		result.Logs.InlineTruncated = result.Logs.InlineTruncated || stdout.inlineTruncated
		result.Logs.Truncated = result.Logs.Truncated || stdout.contentTruncated
	}

	stderr, err := m.processLog(ctx, runID, result, storage.ArtifactKindStderr, result.Logs.Stderr, originalLogsTruncated)
	if err != nil {
		return contracts.RunResult{}, err
	}
	if stderr.ref != nil {
		result.Logs.Stderr = stderr.inline
		result.Logs.StderrArtifact = stderr.ref
		result.Logs.InlineTruncated = result.Logs.InlineTruncated || stderr.inlineTruncated
		result.Logs.Truncated = result.Logs.Truncated || stderr.contentTruncated
	}

	return result, nil
}

type processedLog struct {
	ref              *contracts.ArtifactReference
	inline           string
	inlineTruncated  bool
	contentTruncated bool
}

func (m *Manager) processLog(ctx context.Context, runID string, result contracts.RunResult, kind storage.ArtifactKind, content string, alreadyTruncated bool) (processedLog, error) {
	if content == "" {
		return processedLog{}, nil
	}
	raw := []byte(content)
	inlineMax := m.cfg.InlineMaxBytes
	if len(raw) <= inlineMax {
		return processedLog{}, nil
	}

	contentToStore := raw
	contentTruncated := false
	if m.cfg.MaxBytes > 0 && len(contentToStore) > m.cfg.MaxBytes {
		contentToStore = contentToStore[:m.cfg.MaxBytes]
		contentTruncated = true
	}

	inline := ""
	if inlineMax > 0 {
		if len(contentToStore) < inlineMax {
			inline = string(contentToStore)
		} else {
			inline = string(contentToStore[:inlineMax])
		}
	}

	artifactID := storage.StableUUID("artifact", result.AttemptID, string(kind))
	key, err := ArtifactKey(m.cfg.KeyPrefix, runID, result.AttemptID, kind)
	if err != nil {
		return processedLog{}, err
	}

	stored, err := m.store.Put(ctx, objectstore.Object{
		Bucket:      m.cfg.Bucket,
		Key:         key,
		ContentType: textLogContentType,
		Content:     contentToStore,
		Metadata: map[string]string{
			"submission_id": result.SubmissionID,
			"attempt_id":    result.AttemptID,
			"run_id":        runID,
			"kind":          string(kind),
		},
	})
	if err != nil {
		return processedLog{}, err
	}

	return processedLog{
		ref: &contracts.ArtifactReference{
			ID:          artifactID,
			Kind:        string(kind),
			Provider:    stored.Provider,
			Bucket:      stored.Bucket,
			Key:         stored.Key,
			ContentType: stored.ContentType,
			SHA256:      stored.SHA256,
			SizeBytes:   stored.SizeBytes,
			Truncated:   alreadyTruncated || contentTruncated,
		},
		inline:           inline,
		inlineTruncated:  len(raw) > len([]byte(inline)),
		contentTruncated: contentTruncated,
	}, nil
}

func ArtifactKey(prefix string, runID string, attemptID string, kind storage.ArtifactKind) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run id is required")
	}
	if attemptID == "" {
		return "", fmt.Errorf("attempt id is required")
	}
	key := path.Join(strings.Trim(prefix, "/"), "runs", runID, "attempts", attemptID, string(kind)+".txt")
	return objectstore.NormalizeKey(key)
}

func utcNow() time.Time {
	return time.Now().UTC()
}
