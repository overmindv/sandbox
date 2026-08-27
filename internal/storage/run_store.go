package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
)

var (
	ErrAttemptInProgress   = errors.New("attempt is already in progress")
	ErrAttemptConflict     = errors.New("attempt id belongs to another submission")
	ErrAttemptNotFound     = errors.New("attempt not found")
	ErrIdempotencyConflict = errors.New("idempotency key belongs to another attempt")
	ErrOutboxEventNotFound = errors.New("outbox event not found")
)

type AttemptReservationState string

const (
	AttemptReserved           AttemptReservationState = "reserved"
	AttemptDuplicateCompleted AttemptReservationState = "duplicate_completed"
	AttemptDuplicateRunning   AttemptReservationState = "duplicate_running"
)

type AttemptReservation struct {
	State  AttemptReservationState
	Result contracts.RunResult
}

type IdempotencyKeyBinding struct {
	Key       string
	AttemptID string
	Created   bool
}

type RunStore interface {
	ReserveAttempt(ctx context.Context, req contracts.RunRequest, workerID string) (AttemptReservation, error)
	CompleteAttempt(ctx context.Context, result contracts.RunResult) error
	FailAttempt(ctx context.Context, failure AttemptFailure) error
	GetAttemptResult(ctx context.Context, attemptID string) (contracts.RunResult, bool, error)
	BindIdempotencyKey(ctx context.Context, key string, attemptID string) (IdempotencyKeyBinding, error)
	UpsertWorkerInstance(ctx context.Context, worker WorkerInstanceRecord) error
	ReplaceWorkerCapabilities(ctx context.Context, workerID string, capabilities []WorkerCapabilityRecord) error
	GetWorkerInstance(ctx context.Context, workerID string) (WorkerInstanceRecord, bool, error)
	ListWorkerCapabilities(ctx context.Context, workerID string) ([]WorkerCapabilityRecord, error)
	GetRunBySubmission(ctx context.Context, submissionID string) (RunRecord, bool, error)
	GetAttempt(ctx context.Context, attemptID string) (AttemptRecord, bool, error)
	ListRunFiles(ctx context.Context, runID string) ([]RunFileRecord, error)
	ListRunTests(ctx context.Context, runID string) ([]RunTestRecord, error)
	ListAttemptTestResults(ctx context.Context, attemptID string) ([]AttemptTestResultRecord, error)
	ListArtifacts(ctx context.Context, runID string, attemptID string) ([]ArtifactRecord, error)
	AppendOutboxEvent(ctx context.Context, event OutboxEventRecord) (OutboxEventRecord, error)
	ListOutboxEvents(ctx context.Context, filter OutboxFilter) ([]OutboxEventRecord, error)
	MarkOutboxPublished(ctx context.Context, eventID string, publishedAt time.Time) error
	Close() error
}

func NewRunStore(ctx context.Context, cfg config.StorageConfig) (RunStore, error) {
	switch cfg.Type {
	case "", "memory":
		return NewMemoryRunStore(), nil
	case "postgres":
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return NewPostgresRunStore(pingCtx, cfg.Postgres)
	default:
		return nil, fmt.Errorf("unsupported storage type %q", cfg.Type)
	}
}
