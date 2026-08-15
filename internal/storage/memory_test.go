package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/storage"
)

func TestMemoryRunStoreReserveCompleteAndDuplicateResult(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()

	reservation, err := store.ReserveAttempt(context.Background(), req, "worker-1")
	if err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}
	if reservation.State != storage.AttemptReserved {
		t.Errorf("expected reserved state, got %q", reservation.State)
	}

	result := validStorageRunResult(req)
	if err := store.CompleteAttempt(context.Background(), result); err != nil {
		t.Errorf("complete attempt: %v", err)
		return
	}

	duplicate, err := store.ReserveAttempt(context.Background(), req, "worker-2")
	if err != nil {
		t.Errorf("reserve duplicate attempt: %v", err)
		return
	}
	if duplicate.State != storage.AttemptDuplicateCompleted {
		t.Errorf("expected duplicate completed state, got %q", duplicate.State)
	}
	if duplicate.Result.AttemptID != req.AttemptID || duplicate.Result.Status != contracts.StatusPassed {
		t.Errorf("expected known passed result, got %#v", duplicate.Result)
	}

	known, ok, err := store.GetAttemptResult(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get attempt result: %v", err)
		return
	}
	if !ok {
		t.Error("expected stored result to be found")
	}
	if known.AttemptID != req.AttemptID || known.Status != contracts.StatusPassed {
		t.Errorf("expected stored passed result, got %#v", known)
	}
}

func TestMemoryRunStoreDuplicateRunningAttempt(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()

	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}

	duplicate, err := store.ReserveAttempt(context.Background(), req, "worker-2")
	if err != nil {
		t.Errorf("reserve duplicate attempt: %v", err)
		return
	}
	if duplicate.State != storage.AttemptDuplicateRunning {
		t.Errorf("expected duplicate running state, got %q", duplicate.State)
	}
}

func TestMemoryRunStoreAttemptConflict(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()
	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}

	conflicting := req
	conflicting.SubmissionID = "55555555-5555-4555-8555-555555555555"
	_, err := store.ReserveAttempt(context.Background(), conflicting, "worker-2")
	if !errors.Is(err, storage.ErrAttemptConflict) {
		t.Errorf("expected attempt conflict, got %v", err)
	}
}

func TestMemoryRunStoreIdempotencyKeyBinding(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()
	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}

	binding, err := store.BindIdempotencyKey(context.Background(), "key-1", req.AttemptID)
	if err != nil {
		t.Errorf("bind idempotency key: %v", err)
		return
	}
	if !binding.Created {
		t.Error("expected first binding to be created")
	}

	binding, err = store.BindIdempotencyKey(context.Background(), "key-1", req.AttemptID)
	if err != nil {
		t.Errorf("bind same idempotency key: %v", err)
		return
	}
	if binding.Created {
		t.Error("expected existing binding not to be created again")
	}

	_, err = store.BindIdempotencyKey(context.Background(), "key-1", "66666666-6666-4666-8666-666666666666")
	if !errors.Is(err, storage.ErrIdempotencyConflict) {
		t.Errorf("expected idempotency conflict, got %v", err)
	}
}

func validStorageRunRequest() contracts.RunRequest {
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
		},
		Tests: contracts.TestSpec{
			Mode:   contracts.TestModePytest,
			Source: contracts.TestSourceInline,
		},
		TraceID:   "trace-storage",
		CreatedAt: time.Now().UTC(),
	}
}

func validStorageRunResult(req contracts.RunRequest) contracts.RunResult {
	exitCode := 0
	return contracts.RunResult{
		SchemaVersion: contracts.RunResultSchemaVersion,
		SubmissionID:  req.SubmissionID,
		AttemptID:     req.AttemptID,
		UserID:        req.UserID,
		TaskID:        req.TaskID,
		Language:      req.Language,
		Status:        contracts.StatusPassed,
		Summary: contracts.ResultSummary{
			TestsTotal:  1,
			TestsPassed: 1,
		},
		Resources: contracts.ResultResources{
			ExitCode: &exitCode,
		},
		TraceID:   req.TraceID,
		WorkerID:  "worker-1",
		CreatedAt: time.Now().UTC(),
	}
}
