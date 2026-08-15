package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
)

const postgresDriverName = "pgx"

type PostgresRunStore struct {
	db  *sql.DB
	now func() time.Time
}

func NewPostgresRunStore(ctx context.Context, cfg config.PostgresStore) (*PostgresRunStore, error) {
	if cfg.DSN == "" {
		return nil, errors.New("postgres dsn is required")
	}

	db, err := sql.Open(postgresDriverName, cfg.DSN)
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConnections > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConnections)
	}
	if cfg.MaxIdleConnections > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConnections)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PostgresRunStore{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *PostgresRunStore) ReserveAttempt(ctx context.Context, req contracts.RunRequest, workerID string) (AttemptReservation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AttemptReservation{}, err
	}
	defer rollback(tx)

	now := s.now()
	if err := upsertRun(ctx, tx, req, now); err != nil {
		return AttemptReservation{}, err
	}

	requestJSON, err := json.Marshal(req)
	if err != nil {
		return AttemptReservation{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO attempts (
			attempt_id,
			submission_id,
			status,
			request_json,
			worker_id,
			started_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, 'running', $3, $4, $5, $5, $5)
		ON CONFLICT (attempt_id) DO NOTHING
	`, req.AttemptID, req.SubmissionID, requestJSON, workerID, now)
	if err != nil {
		return AttemptReservation{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return AttemptReservation{}, err
	}
	if rowsAffected == 1 {
		if err := tx.Commit(); err != nil {
			return AttemptReservation{}, err
		}
		return AttemptReservation{State: AttemptReserved}, nil
	}

	var existingSubmissionID string
	var resultJSON []byte
	err = tx.QueryRowContext(ctx, `
		SELECT submission_id::text, result_json
		FROM attempts
		WHERE attempt_id = $1
	`, req.AttemptID).Scan(&existingSubmissionID, &resultJSON)
	if err != nil {
		return AttemptReservation{}, err
	}
	if existingSubmissionID != req.SubmissionID {
		return AttemptReservation{}, ErrAttemptConflict
	}

	if len(resultJSON) == 0 {
		if err := tx.Commit(); err != nil {
			return AttemptReservation{}, err
		}
		return AttemptReservation{State: AttemptDuplicateRunning}, nil
	}

	var knownResult contracts.RunResult
	if err := json.Unmarshal(resultJSON, &knownResult); err != nil {
		return AttemptReservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AttemptReservation{}, err
	}
	return AttemptReservation{State: AttemptDuplicateCompleted, Result: knownResult}, nil
}

func (s *PostgresRunStore) CompleteAttempt(ctx context.Context, result contracts.RunResult) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	now := s.now()
	execResult, err := tx.ExecContext(ctx, `
		UPDATE attempts
		SET
			status = 'completed',
			result_json = $2,
			worker_id = $3,
			completed_at = $4,
			updated_at = $4
		WHERE attempt_id = $1 AND submission_id = $5
	`, result.AttemptID, resultJSON, result.WorkerID, now, result.SubmissionID)
	if err != nil {
		return err
	}
	rowsAffected, err := execResult.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrAttemptNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE runs
		SET updated_at = $2
		WHERE submission_id = $1
	`, result.SubmissionID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresRunStore) GetAttemptResult(ctx context.Context, attemptID string) (contracts.RunResult, bool, error) {
	var resultJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT result_json
		FROM attempts
		WHERE attempt_id = $1
	`, attemptID).Scan(&resultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.RunResult{}, false, nil
	}
	if err != nil {
		return contracts.RunResult{}, false, err
	}
	if len(resultJSON) == 0 {
		return contracts.RunResult{}, false, nil
	}

	var result contracts.RunResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return contracts.RunResult{}, false, err
	}
	return result, true, nil
}

func (s *PostgresRunStore) BindIdempotencyKey(ctx context.Context, key string, attemptID string) (IdempotencyKeyBinding, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO idempotency_keys (idempotency_key, attempt_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, key, attemptID, s.now())
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}
	if rowsAffected == 1 {
		return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: true}, nil
	}

	var existingAttemptID string
	err = s.db.QueryRowContext(ctx, `
		SELECT attempt_id::text
		FROM idempotency_keys
		WHERE idempotency_key = $1
	`, key).Scan(&existingAttemptID)
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}
	if existingAttemptID != attemptID {
		return IdempotencyKeyBinding{}, ErrIdempotencyConflict
	}
	return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: false}, nil
}

func (s *PostgresRunStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func upsertRun(ctx context.Context, tx *sql.Tx, req contracts.RunRequest, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO runs (
			submission_id,
			user_id,
			task_id,
			language_id,
			language_version,
			first_attempt_id,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (submission_id) DO UPDATE
		SET updated_at = EXCLUDED.updated_at
	`, req.SubmissionID, req.UserID, req.TaskID, req.Language.ID, req.Language.Version, req.AttemptID, now)
	return err
}

func rollback(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

var _ RunStore = (*PostgresRunStore)(nil)
