package storage_test

import (
	"context"
	"encoding/base64"
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

func TestMemoryRunStoreRetryableFailureCanBeReservedAgain(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()

	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}
	if err := store.FailAttempt(context.Background(), storage.AttemptFailure{
		AttemptID:        req.AttemptID,
		SubmissionID:     req.SubmissionID,
		WorkerInstanceID: "worker-1",
		Status:           storage.AttemptStatusFailedInternal,
		ErrorClass:       "infra",
		Reason:           "gojudge_unavailable",
		Message:          "dial tcp failed",
		DeliveryAttempts: 1,
		FailedAt:         time.Now().UTC(),
	}); err != nil {
		t.Errorf("fail attempt: %v", err)
		return
	}

	attempt, ok, err := store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get failed attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected failed attempt")
		return
	}
	if attempt.Status != storage.AttemptStatusFailedInternal {
		t.Errorf("expected failed_internal status, got %q", attempt.Status)
	}
	if attempt.LastErrorReason != "gojudge_unavailable" {
		t.Errorf("expected last error reason, got %#v", attempt)
	}

	reservation, err := store.ReserveAttempt(context.Background(), req, "worker-2")
	if err != nil {
		t.Errorf("reserve retry attempt: %v", err)
		return
	}
	if reservation.State != storage.AttemptReserved {
		t.Errorf("expected retry reservation, got %q", reservation.State)
	}

	attempt, ok, err = store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get retried attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected retried attempt")
		return
	}
	if attempt.Status != storage.AttemptStatusRunning {
		t.Errorf("expected running status after retry reservation, got %q", attempt.Status)
	}
	if attempt.WorkerInstanceID != "worker-2" {
		t.Errorf("expected retry worker id, got %q", attempt.WorkerInstanceID)
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

func TestMemoryRunStoreCreatesProductionSnapshot(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()

	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}

	run, ok, err := store.GetRunBySubmission(context.Background(), req.SubmissionID)
	if err != nil {
		t.Errorf("get run by submission: %v", err)
		return
	}
	if !ok {
		t.Error("expected run to be stored")
		return
	}
	if run.RunID == "" {
		t.Error("expected run id to be generated")
	}
	if len(run.RequestHash) != 64 {
		t.Errorf("expected sha256 request hash, got %q", run.RequestHash)
	}

	attempt, ok, err := store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected attempt to be stored")
		return
	}
	if attempt.RunID != run.RunID {
		t.Errorf("expected attempt run id %q, got %q", run.RunID, attempt.RunID)
	}
	if attempt.AttemptNo != 1 {
		t.Errorf("expected first attempt number, got %d", attempt.AttemptNo)
	}

	files, err := store.ListRunFiles(context.Background(), run.RunID)
	if err != nil {
		t.Errorf("list run files: %v", err)
		return
	}
	if len(files) != 2 {
		t.Errorf("expected source and test file snapshots, got %d", len(files))
		return
	}
	if files[0].Kind != storage.RunFileKindSource || files[1].Kind != storage.RunFileKindTest {
		t.Errorf("expected source/test file kinds, got %#v", files)
	}

	tests, err := store.ListRunTests(context.Background(), run.RunID)
	if err != nil {
		t.Errorf("list run tests: %v", err)
		return
	}
	if len(tests) != 1 {
		t.Errorf("expected one run test snapshot, got %d", len(tests))
		return
	}
	if tests[0].Name != "test_solution.py" {
		t.Errorf("expected test file name, got %q", tests[0].Name)
	}
}

func TestMemoryRunStoreStoresAttemptResultsArtifactsAndOutbox(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()

	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve attempt: %v", err)
		return
	}

	result := validStorageRunResult(req)
	result.Logs = contracts.ResultLogs{
		Stdout:    "ok\n",
		Stderr:    "warning\n",
		Truncated: true,
	}
	result.Tests = []contracts.TestCaseResult{
		{Name: "test_add", Status: "passed", DurationMS: 3},
		{Name: "test_subtract", Status: "failed", DurationMS: 5, Message: "expected 1"},
	}
	if err := store.CompleteAttempt(context.Background(), result); err != nil {
		t.Errorf("complete attempt: %v", err)
		return
	}

	attempt, ok, err := store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected attempt to be stored")
		return
	}
	if attempt.Status != storage.AttemptStatusCompleted {
		t.Errorf("expected completed attempt, got %q", attempt.Status)
	}

	testResults, err := store.ListAttemptTestResults(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("list attempt test results: %v", err)
		return
	}
	if len(testResults) != 2 {
		t.Errorf("expected two test result rows, got %d", len(testResults))
		return
	}
	if testResults[1].Status != "failed" {
		t.Errorf("expected failed second test row, got %#v", testResults[1])
	}

	artifacts, err := store.ListArtifacts(context.Background(), attempt.RunID, req.AttemptID)
	if err != nil {
		t.Errorf("list artifacts: %v", err)
		return
	}
	if len(artifacts) != 2 {
		t.Errorf("expected stdout and stderr artifacts, got %d", len(artifacts))
		return
	}
	if artifacts[0].ContentType == "" || artifacts[1].ContentType == "" {
		t.Errorf("expected artifact content types, got %#v", artifacts)
	}
	if artifacts[0].StorageProvider != storage.ArtifactStoragePostgresJSON {
		t.Errorf("expected inline artifact metadata provider, got %q", artifacts[0].StorageProvider)
	}

	event, err := storage.ResultOutboxEvent(attempt.RunID, result, "code-execution.results.v1", time.Now().UTC())
	if err != nil {
		t.Errorf("build outbox event: %v", err)
		return
	}
	if _, err := store.AppendOutboxEvent(context.Background(), event); err != nil {
		t.Errorf("append outbox event: %v", err)
		return
	}

	pending, err := store.ListOutboxEvents(context.Background(), storage.OutboxFilter{Status: storage.OutboxStatusPending})
	if err != nil {
		t.Errorf("list pending outbox: %v", err)
		return
	}
	if len(pending) != 1 {
		t.Errorf("expected one pending outbox event, got %d", len(pending))
		return
	}
	if pending[0].EventType != storage.OutboxEventRunResultReady {
		t.Errorf("expected result outbox event, got %q", pending[0].EventType)
	}

	if err := store.MarkOutboxPublished(context.Background(), pending[0].EventID, time.Now().UTC()); err != nil {
		t.Errorf("mark outbox published: %v", err)
		return
	}
	pending, err = store.ListOutboxEvents(context.Background(), storage.OutboxFilter{Status: storage.OutboxStatusPending})
	if err != nil {
		t.Errorf("list pending outbox after publish: %v", err)
		return
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending outbox events after publish, got %d", len(pending))
	}
}

func TestMemoryRunStoreWorkerInventory(t *testing.T) {
	store := storage.NewMemoryRunStore()
	now := time.Now().UTC()

	if err := store.UpsertWorkerInstance(context.Background(), storage.WorkerInstanceRecord{
		WorkerInstanceID: "worker-1",
		Hostname:         "host-1",
		Environment:      "test",
		ExecutorType:     "fake",
		Status:           storage.WorkerStatusActive,
		StartedAt:        now,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Errorf("upsert worker instance: %v", err)
		return
	}
	if err := store.ReplaceWorkerCapabilities(context.Background(), "worker-1", []storage.WorkerCapabilityRecord{
		{
			LanguageID:      "python",
			LanguageVersion: "*",
			MaxConcurrency:  4,
		},
		{
			LanguageID:      "go",
			LanguageVersion: "*",
			MaxConcurrency:  2,
		},
	}); err != nil {
		t.Errorf("replace worker capabilities: %v", err)
		return
	}

	worker, ok, err := store.GetWorkerInstance(context.Background(), "worker-1")
	if err != nil {
		t.Errorf("get worker instance: %v", err)
		return
	}
	if !ok {
		t.Error("expected worker instance")
		return
	}
	if worker.ExecutorType != "fake" || worker.Status != storage.WorkerStatusActive {
		t.Errorf("expected active fake worker, got %#v", worker)
	}

	capabilities, err := store.ListWorkerCapabilities(context.Background(), "worker-1")
	if err != nil {
		t.Errorf("list worker capabilities: %v", err)
		return
	}
	if len(capabilities) != 2 {
		t.Errorf("expected two capabilities, got %d", len(capabilities))
		return
	}
	if capabilities[0].LanguageID != "go" || capabilities[1].LanguageID != "python" {
		t.Errorf("expected capabilities sorted by language, got %#v", capabilities)
	}
}

func TestMemoryRunStoreGroupsEquivalentAttemptsUnderSameRun(t *testing.T) {
	store := storage.NewMemoryRunStore()
	req := validStorageRunRequest()
	second := req
	second.AttemptID = "77777777-7777-4777-8777-777777777777"
	second.TraceID = "trace-storage-second"
	second.CreatedAt = req.CreatedAt.Add(time.Minute)

	if _, err := store.ReserveAttempt(context.Background(), req, "worker-1"); err != nil {
		t.Errorf("reserve first attempt: %v", err)
		return
	}
	if _, err := store.ReserveAttempt(context.Background(), second, "worker-2"); err != nil {
		t.Errorf("reserve second attempt: %v", err)
		return
	}

	firstAttempt, ok, err := store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get first attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected first attempt")
		return
	}
	secondAttempt, ok, err := store.GetAttempt(context.Background(), second.AttemptID)
	if err != nil {
		t.Errorf("get second attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected second attempt")
		return
	}
	if firstAttempt.RunID != secondAttempt.RunID {
		t.Errorf("expected equivalent attempts to share run id, got %q and %q", firstAttempt.RunID, secondAttempt.RunID)
	}
	if firstAttempt.RequestHash != secondAttempt.RequestHash {
		t.Errorf("expected equivalent attempts to share request hash, got %q and %q", firstAttempt.RequestHash, secondAttempt.RequestHash)
	}
	if secondAttempt.AttemptNo != 2 {
		t.Errorf("expected second attempt number, got %d", secondAttempt.AttemptNo)
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
			Files: []contracts.SourceFile{
				{
					Path:       "solution.py",
					ContentB64: base64.StdEncoding.EncodeToString([]byte("def add(a, b):\n    return a + b\n")),
				},
			},
		},
		Tests: contracts.TestSpec{
			Mode:       contracts.TestModePytest,
			Source:     contracts.TestSourceInline,
			Entrypoint: "test_solution.py",
			Files: []contracts.SourceFile{
				{
					Path:       "test_solution.py",
					ContentB64: base64.StdEncoding.EncodeToString([]byte("from solution import add\n")),
				},
			},
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
