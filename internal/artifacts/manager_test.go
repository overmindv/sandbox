package artifacts

import (
	"context"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
)

func TestManagerStoresLargeLogsAsArtifacts(t *testing.T) {
	store, err := objectstore.NewFilesystemStore(objectstore.FilesystemConfig{
		RootDir: t.TempDir(),
		Bucket:  "local",
	})
	if err != nil {
		t.Errorf("create filesystem store: %v", err)
		return
	}

	manager, err := NewManager(config.ArtifactStorageConfig{
		Enabled:        true,
		Provider:       "filesystem",
		Bucket:         "local",
		KeyPrefix:      "artifacts",
		InlineMaxBytes: 5,
		MaxBytes:       12,
	}, store)
	if err != nil {
		t.Errorf("create artifact manager: %v", err)
		return
	}

	result := validArtifactRunResult()
	result.Logs.Stdout = "abcdefghijklmnopqrstuvwxyz"
	processed, err := manager.ProcessResult(context.Background(), "11111111-1111-5111-8111-111111111111", result)
	if err != nil {
		t.Errorf("process result: %v", err)
		return
	}

	if processed.Logs.Stdout != "abcde" {
		t.Errorf("expected inline stdout preview, got %q", processed.Logs.Stdout)
	}
	if !processed.Logs.InlineTruncated {
		t.Error("expected inline_truncated=true")
	}
	if !processed.Logs.Truncated {
		t.Error("expected logs.truncated=true when uploaded content is capped")
	}
	if processed.Logs.StdoutArtifact == nil {
		t.Error("expected stdout artifact reference")
		return
	}
	ref := processed.Logs.StdoutArtifact
	if ref.Provider != "filesystem" || ref.Bucket != "local" {
		t.Errorf("unexpected artifact provider metadata: %#v", ref)
	}
	if ref.SizeBytes != 12 {
		t.Errorf("expected capped artifact size 12, got %d", ref.SizeBytes)
	}
	if !ref.Truncated {
		t.Error("expected artifact truncated flag")
	}
	if !strings.Contains(ref.Key, "/stdout.txt") {
		t.Errorf("expected stdout artifact key, got %q", ref.Key)
	}

	stored, err := store.Get(context.Background(), ref.Bucket, ref.Key)
	if err != nil {
		t.Errorf("get stored artifact: %v", err)
		return
	}
	if string(stored) != "abcdefghijkl" {
		t.Errorf("expected capped artifact body, got %q", string(stored))
	}
	if ref.SHA256 != objectstore.Digest(stored) {
		t.Errorf("expected artifact digest to match stored content, got %q", ref.SHA256)
	}
}

func TestManagerLeavesSmallLogsInline(t *testing.T) {
	store := objectstore.NewMemoryStore("local")
	manager, err := NewManager(config.ArtifactStorageConfig{
		Enabled:        true,
		Provider:       "filesystem",
		Bucket:         "local",
		KeyPrefix:      "artifacts",
		InlineMaxBytes: 32,
		MaxBytes:       1024,
	}, store)
	if err != nil {
		t.Errorf("create artifact manager: %v", err)
		return
	}

	result := validArtifactRunResult()
	result.Logs.Stdout = "small\n"
	processed, err := manager.ProcessResult(context.Background(), "11111111-1111-5111-8111-111111111111", result)
	if err != nil {
		t.Errorf("process result: %v", err)
		return
	}
	if processed.Logs.Stdout != "small\n" {
		t.Errorf("expected stdout to stay inline, got %q", processed.Logs.Stdout)
	}
	if processed.Logs.StdoutArtifact != nil {
		t.Errorf("expected no artifact for small stdout, got %#v", processed.Logs.StdoutArtifact)
	}
}

func validArtifactRunResult() contracts.RunResult {
	return contracts.RunResult{
		DiagnosticsVisibility: "public",
		SchemaVersion:         contracts.RunResultSchemaVersion,
		SubmissionID:          "22222222-2222-4222-8222-222222222222",
		AttemptID:             "33333333-3333-4333-8333-333333333333",
		UserID:                "44444444-4444-4444-8444-444444444444",
		TaskID:                "55555555-5555-4555-8555-555555555555",
		Language: contracts.LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Status: contracts.StatusPassed,
		Summary: contracts.ResultSummary{
			TestsTotal:  1,
			TestsPassed: 1,
		},
		Logs:      contracts.ResultLogs{},
		Error:     nil,
		TraceID:   "trace-artifacts",
		WorkerID:  "worker-1",
		CreatedAt: time.Now().UTC(),
	}
}

func TestPrivateLogsNeverBecomeArtifacts(t *testing.T) {
	store, err := objectstore.NewFilesystemStore(objectstore.FilesystemConfig{RootDir: t.TempDir(), Bucket: "local"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(config.ArtifactStorageConfig{Enabled: true, Provider: "filesystem", Bucket: "local", InlineMaxBytes: 0, MaxBytes: 1024}, store)
	if err != nil {
		t.Fatal(err)
	}
	result := validArtifactRunResult()
	result.DiagnosticsVisibility = "private"
	result.Logs.Stdout = "SECRET"
	result.Logs.Stderr = "SECRET"
	got, err := m.ProcessResult(context.Background(), "run", result)
	if err != nil {
		t.Fatal(err)
	}
	if got.Logs.Stdout != "" || got.Logs.Stderr != "" || got.Logs.StdoutArtifact != nil || got.Logs.StderrArtifact != nil {
		t.Fatal("private artifact created")
	}
}
