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
	if err := upsertWorkerInstanceTx(ctx, tx, WorkerInstanceRecord{
		WorkerInstanceID: workerID,
		Hostname:         workerID,
		Status:           WorkerStatusActive,
		StartedAt:        now,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, now); err != nil {
		return AttemptReservation{}, err
	}

	snapshot, err := BuildRunSnapshot(req, now)
	if err != nil {
		return AttemptReservation{}, err
	}
	if err := upsertRunTx(ctx, tx, snapshot.Run); err != nil {
		return AttemptReservation{}, err
	}
	if err := insertRunFilesTx(ctx, tx, snapshot.Files); err != nil {
		return AttemptReservation{}, err
	}
	if err := insertRunTestsTx(ctx, tx, snapshot.Tests); err != nil {
		return AttemptReservation{}, err
	}

	var lockedRunID string
	if err := tx.QueryRowContext(ctx, `
		SELECT run_id::text
		FROM runs
		WHERE run_id = $1
		FOR UPDATE
	`, snapshot.RunID).Scan(&lockedRunID); err != nil {
		return AttemptReservation{}, err
	}

	requestJSON, err := json.Marshal(req)
	if err != nil {
		return AttemptReservation{}, err
	}
	attemptNo, err := nextPostgresAttemptNo(ctx, tx, snapshot.RunID)
	if err != nil {
		return AttemptReservation{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO attempts (
			attempt_id,
			run_id,
			submission_id,
			attempt_no,
			status,
			request_hash,
			request_json,
			worker_instance_id,
			started_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $9)
		ON CONFLICT (attempt_id) DO NOTHING
	`, req.AttemptID, snapshot.RunID, req.SubmissionID, attemptNo, AttemptStatusRunning, snapshot.RequestHash, requestJSON, workerID, now)
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
	if result.WorkerID != "" {
		if err := upsertWorkerInstanceTx(ctx, tx, WorkerInstanceRecord{
			WorkerInstanceID: result.WorkerID,
			Hostname:         result.WorkerID,
			Status:           WorkerStatusActive,
			LastSeenAt:       now,
			CreatedAt:        now,
			UpdatedAt:        now,
		}, now); err != nil {
			return err
		}
	}

	var runID string
	err = tx.QueryRowContext(ctx, `
		UPDATE attempts
		SET
			status = $2,
			result_json = $3,
			worker_instance_id = NULLIF($4, ''),
			completed_at = $5,
			updated_at = $5
		WHERE attempt_id = $1 AND submission_id = $6
		RETURNING run_id::text
	`, result.AttemptID, AttemptStatusCompleted, resultJSON, result.WorkerID, now, result.SubmissionID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return attemptNotFoundOrConflict(ctx, tx, result.AttemptID, result.SubmissionID)
	}
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE runs
		SET status = $2, completed_at = COALESCE(completed_at, $3), updated_at = $3
		WHERE run_id = $1
	`, runID, RunStatusCompleted, now); err != nil {
		return err
	}

	if err := insertAttemptTestResultsTx(ctx, tx, AttemptTestResultsFromRunResult(result, now)); err != nil {
		return err
	}
	if err := insertArtifactsTx(ctx, tx, ArtifactsFromRunResult(runID, result, now)); err != nil {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}
	defer rollback(tx)

	var existingAttemptID string
	err = tx.QueryRowContext(ctx, `
		SELECT attempt_id::text
		FROM idempotency_keys
		WHERE idempotency_key = $1
	`, key).Scan(&existingAttemptID)
	if err == nil {
		if existingAttemptID != attemptID {
			return IdempotencyKeyBinding{}, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return IdempotencyKeyBinding{}, err
		}
		return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: false}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IdempotencyKeyBinding{}, err
	}

	var runID string
	var requestHash string
	err = tx.QueryRowContext(ctx, `
		SELECT run_id::text, request_hash
		FROM attempts
		WHERE attempt_id = $1
	`, attemptID).Scan(&runID, &requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotencyKeyBinding{}, ErrAttemptNotFound
	}
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO idempotency_keys (
			idempotency_key,
			run_id,
			attempt_id,
			request_hash,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, key, runID, attemptID, requestHash, s.now())
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return IdempotencyKeyBinding{}, err
	}
	if rowsAffected == 1 {
		if err := tx.Commit(); err != nil {
			return IdempotencyKeyBinding{}, err
		}
		return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: true}, nil
	}

	err = tx.QueryRowContext(ctx, `
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
	if err := tx.Commit(); err != nil {
		return IdempotencyKeyBinding{}, err
	}
	return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: false}, nil
}

func (s *PostgresRunStore) UpsertWorkerInstance(ctx context.Context, worker WorkerInstanceRecord) error {
	return upsertWorkerInstanceDB(ctx, s.db, worker, s.now())
}

func (s *PostgresRunStore) ReplaceWorkerCapabilities(ctx context.Context, workerID string, capabilities []WorkerCapabilityRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_capabilities WHERE worker_instance_id = $1`, workerID); err != nil {
		return err
	}
	for _, capability := range capabilities {
		if capability.WorkerInstanceID == "" {
			capability.WorkerInstanceID = workerID
		}
		if err := insertWorkerCapabilityTx(ctx, tx, capability, s.now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresRunStore) GetWorkerInstance(ctx context.Context, workerID string) (WorkerInstanceRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			worker_instance_id,
			COALESCE(hostname, ''),
			COALESCE(environment, ''),
			COALESCE(executor_type, ''),
			COALESCE(version, ''),
			status,
			started_at,
			last_seen_at,
			stopped_at,
			created_at,
			updated_at
		FROM worker_instances
		WHERE worker_instance_id = $1
	`, workerID)
	record, err := scanWorkerInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerInstanceRecord{}, false, nil
	}
	if err != nil {
		return WorkerInstanceRecord{}, false, err
	}
	return record, true, nil
}

func (s *PostgresRunStore) ListWorkerCapabilities(ctx context.Context, workerID string) ([]WorkerCapabilityRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT worker_instance_id, language_id, language_version, max_concurrency, created_at, updated_at
		FROM worker_capabilities
		WHERE worker_instance_id = $1
		ORDER BY language_id, language_version
	`, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []WorkerCapabilityRecord
	for rows.Next() {
		var record WorkerCapabilityRecord
		if err := rows.Scan(&record.WorkerInstanceID, &record.LanguageID, &record.LanguageVersion, &record.MaxConcurrency, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresRunStore) GetRunBySubmission(ctx context.Context, submissionID string) (RunRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			run_id::text,
			submission_id::text,
			user_id::text,
			task_id::text,
			COALESCE(task_version_id::text, ''),
			language_id,
			language_version,
			status,
			request_hash,
			limits_profile_id,
			created_at,
			updated_at,
			completed_at
		FROM runs
		WHERE submission_id = $1
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 1
	`, submissionID)
	record, err := scanRunRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RunRecord{}, false, nil
	}
	if err != nil {
		return RunRecord{}, false, err
	}
	return record, true, nil
}

func (s *PostgresRunStore) GetAttempt(ctx context.Context, attemptID string) (AttemptRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			attempt_id::text,
			run_id::text,
			submission_id::text,
			attempt_no,
			status,
			request_hash,
			request_json,
			result_json,
			COALESCE(worker_instance_id, ''),
			started_at,
			completed_at,
			lease_expires_at,
			created_at,
			updated_at
		FROM attempts
		WHERE attempt_id = $1
	`, attemptID)
	record, err := scanAttemptRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AttemptRecord{}, false, nil
	}
	if err != nil {
		return AttemptRecord{}, false, err
	}
	return record, true, nil
}

func (s *PostgresRunStore) ListRunFiles(ctx context.Context, runID string) ([]RunFileRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT file_id::text, run_id::text, kind, path, content_hash, COALESCE(object_key, ''), size_bytes, created_at
		FROM run_files
		WHERE run_id = $1
		ORDER BY kind, path
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []RunFileRecord
	for rows.Next() {
		var record RunFileRecord
		if err := rows.Scan(&record.FileID, &record.RunID, &record.Kind, &record.Path, &record.ContentHash, &record.ObjectKey, &record.SizeBytes, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresRunStore) ListRunTests(ctx context.Context, runID string) ([]RunTestRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_test_id::text, run_id::text, COALESCE(test_ref_id, ''), name, source, mode, order_index, test_json, created_at
		FROM run_tests
		WHERE run_id = $1
		ORDER BY order_index
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []RunTestRecord
	for rows.Next() {
		var record RunTestRecord
		var testJSON []byte
		if err := rows.Scan(&record.RunTestID, &record.RunID, &record.TestRefID, &record.Name, &record.Source, &record.Mode, &record.OrderIndex, &testJSON, &record.CreatedAt); err != nil {
			return nil, err
		}
		record.TestJSON = append(json.RawMessage(nil), testJSON...)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresRunStore) ListAttemptTestResults(ctx context.Context, attemptID string) ([]AttemptTestResultRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			attempt_test_result_id::text,
			attempt_id::text,
			COALESCE(run_test_id::text, ''),
			order_index,
			name,
			status,
			duration_ms,
			COALESCE(message, ''),
			COALESCE(stdout_artifact_id::text, ''),
			COALESCE(stderr_artifact_id::text, ''),
			error_json,
			created_at
		FROM attempt_test_results
		WHERE attempt_id = $1
		ORDER BY order_index
	`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []AttemptTestResultRecord
	for rows.Next() {
		var record AttemptTestResultRecord
		var errorJSON []byte
		if err := rows.Scan(
			&record.AttemptTestResultID,
			&record.AttemptID,
			&record.RunTestID,
			&record.OrderIndex,
			&record.Name,
			&record.Status,
			&record.DurationMS,
			&record.Message,
			&record.StdoutArtifactID,
			&record.StderrArtifactID,
			&errorJSON,
			&record.CreatedAt,
		); err != nil {
			return nil, err
		}
		record.ErrorJSON = append(json.RawMessage(nil), errorJSON...)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresRunStore) ListArtifacts(ctx context.Context, runID string, attemptID string) ([]ArtifactRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT artifact_id::text, run_id::text, COALESCE(attempt_id::text, ''), kind, storage_provider, COALESCE(object_key, ''), content_hash, size_bytes, truncated, created_at
		FROM artifacts
		WHERE
			(NULLIF($1, '')::uuid IS NULL OR run_id = NULLIF($1, '')::uuid)
			AND (NULLIF($2, '')::uuid IS NULL OR attempt_id = NULLIF($2, '')::uuid)
		ORDER BY created_at, artifact_id
	`, runID, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ArtifactRecord
	for rows.Next() {
		var record ArtifactRecord
		if err := rows.Scan(&record.ArtifactID, &record.RunID, &record.AttemptID, &record.Kind, &record.StorageProvider, &record.ObjectKey, &record.ContentHash, &record.SizeBytes, &record.Truncated, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresRunStore) AppendOutboxEvent(ctx context.Context, event OutboxEventRecord) (OutboxEventRecord, error) {
	if event.EventID == "" {
		return OutboxEventRecord{}, errors.New("outbox event id is required")
	}
	now := s.now()
	normalizeOutboxEvent(&event, now)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO outbox_events (
			event_id,
			aggregate_type,
			aggregate_id,
			run_id,
			attempt_id,
			event_type,
			topic,
			message_key,
			payload_json,
			headers_json,
			status,
			publish_attempts,
			available_at,
			published_at,
			last_error,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11, $12, $13, $14, NULLIF($15, ''), $16, $17)
		ON CONFLICT (event_id) DO NOTHING
	`, event.EventID, event.AggregateType, event.AggregateID, event.RunID, event.AttemptID, event.EventType, event.Topic, event.Key, event.PayloadJSON, event.HeadersJSON, event.Status, event.PublishAttempts, event.AvailableAt, event.PublishedAt, event.LastError, event.CreatedAt, event.UpdatedAt)
	if err != nil {
		return OutboxEventRecord{}, err
	}

	stored, ok, err := s.getOutboxEvent(ctx, event.EventID)
	if err != nil {
		return OutboxEventRecord{}, err
	}
	if !ok {
		return OutboxEventRecord{}, ErrOutboxEventNotFound
	}
	return stored, nil
}

func (s *PostgresRunStore) ListOutboxEvents(ctx context.Context, filter OutboxFilter) ([]OutboxEventRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			event_id::text,
			aggregate_type,
			aggregate_id::text,
			run_id::text,
			COALESCE(attempt_id::text, ''),
			event_type,
			COALESCE(topic, ''),
			COALESCE(message_key, ''),
			payload_json,
			headers_json,
			status,
			publish_attempts,
			available_at,
			published_at,
			COALESCE(last_error, ''),
			created_at,
			updated_at
		FROM outbox_events
		WHERE ($1 = '' OR status = $1)
		ORDER BY available_at, event_id
		LIMIT $2
	`, filter.Status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []OutboxEventRecord
	for rows.Next() {
		record, err := scanOutboxEvent(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresRunStore) MarkOutboxPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = $2, published_at = $3, updated_at = $3
		WHERE event_id = $1
	`, eventID, OutboxStatusPublished, publishedAt.UTC())
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrOutboxEventNotFound
	}
	return nil
}

func (s *PostgresRunStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func upsertWorkerInstanceDB(ctx context.Context, db *sql.DB, worker WorkerInstanceRecord, now time.Time) error {
	normalizeWorkerInstance(&worker, now)
	_, err := db.ExecContext(ctx, workerInstanceUpsertSQL(), workerInstanceArgs(worker)...)
	return err
}

func upsertWorkerInstanceTx(ctx context.Context, tx *sql.Tx, worker WorkerInstanceRecord, now time.Time) error {
	normalizeWorkerInstance(&worker, now)
	_, err := tx.ExecContext(ctx, workerInstanceUpsertSQL(), workerInstanceArgs(worker)...)
	return err
}

func workerInstanceUpsertSQL() string {
	return `
		INSERT INTO worker_instances (
			worker_instance_id,
			hostname,
			environment,
			executor_type,
			version,
			status,
			started_at,
			last_seen_at,
			stopped_at,
			created_at,
			updated_at
		)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8, $9, $10, $11)
		ON CONFLICT (worker_instance_id) DO UPDATE
		SET
			hostname = COALESCE(EXCLUDED.hostname, worker_instances.hostname),
			environment = COALESCE(EXCLUDED.environment, worker_instances.environment),
			executor_type = COALESCE(EXCLUDED.executor_type, worker_instances.executor_type),
			version = COALESCE(EXCLUDED.version, worker_instances.version),
			status = EXCLUDED.status,
			started_at = COALESCE(worker_instances.started_at, EXCLUDED.started_at),
			last_seen_at = EXCLUDED.last_seen_at,
			stopped_at = EXCLUDED.stopped_at,
			updated_at = EXCLUDED.updated_at
	`
}

func workerInstanceArgs(worker WorkerInstanceRecord) []any {
	return []any{
		worker.WorkerInstanceID,
		worker.Hostname,
		worker.Environment,
		worker.ExecutorType,
		worker.Version,
		worker.Status,
		worker.StartedAt,
		worker.LastSeenAt,
		worker.StoppedAt,
		worker.CreatedAt,
		worker.UpdatedAt,
	}
}

func upsertRunTx(ctx context.Context, tx *sql.Tx, run RunRecord) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO runs (
			run_id,
			submission_id,
			user_id,
			task_id,
			task_version_id,
			language_id,
			language_version,
			status,
			request_hash,
			limits_profile_id,
			created_at,
			updated_at,
			completed_at
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (run_id) DO UPDATE
		SET updated_at = EXCLUDED.updated_at
	`, run.RunID, run.SubmissionID, run.UserID, run.TaskID, run.TaskVersionID, run.LanguageID, run.LanguageVersion, run.Status, run.RequestHash, run.LimitsProfileID, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	return err
}

func insertRunFilesTx(ctx context.Context, tx *sql.Tx, files []RunFileRecord) error {
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_files (
				file_id,
				run_id,
				kind,
				path,
				content_hash,
				object_key,
				size_bytes,
				created_at
			)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
			ON CONFLICT (run_id, kind, path) DO NOTHING
		`, file.FileID, file.RunID, file.Kind, file.Path, file.ContentHash, file.ObjectKey, file.SizeBytes, file.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func insertRunTestsTx(ctx context.Context, tx *sql.Tx, tests []RunTestRecord) error {
	for _, test := range tests {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_tests (
				run_test_id,
				run_id,
				test_ref_id,
				name,
				source,
				mode,
				order_index,
				test_json,
				created_at
			)
			VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9)
			ON CONFLICT (run_id, order_index) DO NOTHING
		`, test.RunTestID, test.RunID, test.TestRefID, test.Name, test.Source, test.Mode, test.OrderIndex, test.TestJSON, test.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func nextPostgresAttemptNo(ctx context.Context, tx *sql.Tx, runID string) (int, error) {
	var attemptNo int
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(attempt_no), 0) + 1
		FROM attempts
		WHERE run_id = $1
	`, runID).Scan(&attemptNo)
	return attemptNo, err
}

func insertAttemptTestResultsTx(ctx context.Context, tx *sql.Tx, records []AttemptTestResultRecord) error {
	for _, record := range records {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attempt_test_results (
				attempt_test_result_id,
				attempt_id,
				run_test_id,
				order_index,
				name,
				status,
				duration_ms,
				message,
				stdout_artifact_id,
				stderr_artifact_id,
				error_json,
				created_at
			)
			VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, '')::uuid, NULLIF($10, '')::uuid, NULLIF($11, 'null')::jsonb, $12)
			ON CONFLICT (attempt_id, order_index) DO UPDATE
			SET
				name = EXCLUDED.name,
				status = EXCLUDED.status,
				duration_ms = EXCLUDED.duration_ms,
				message = EXCLUDED.message,
				stdout_artifact_id = EXCLUDED.stdout_artifact_id,
				stderr_artifact_id = EXCLUDED.stderr_artifact_id,
				error_json = EXCLUDED.error_json
		`, record.AttemptTestResultID, record.AttemptID, record.RunTestID, record.OrderIndex, record.Name, record.Status, record.DurationMS, record.Message, record.StdoutArtifactID, record.StderrArtifactID, nullableJSON(record.ErrorJSON), record.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func insertArtifactsTx(ctx context.Context, tx *sql.Tx, records []ArtifactRecord) error {
	for _, record := range records {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO artifacts (
				artifact_id,
				run_id,
				attempt_id,
				kind,
				storage_provider,
				object_key,
				content_hash,
				size_bytes,
				truncated,
				created_at
			)
			VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, NULLIF($6, ''), $7, $8, $9, $10)
			ON CONFLICT (artifact_id) DO UPDATE
			SET
				storage_provider = EXCLUDED.storage_provider,
				object_key = EXCLUDED.object_key,
				content_hash = EXCLUDED.content_hash,
				size_bytes = EXCLUDED.size_bytes,
				truncated = EXCLUDED.truncated
		`, record.ArtifactID, record.RunID, record.AttemptID, record.Kind, record.StorageProvider, record.ObjectKey, record.ContentHash, record.SizeBytes, record.Truncated, record.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func insertWorkerCapabilityTx(ctx context.Context, tx *sql.Tx, capability WorkerCapabilityRecord, now time.Time) error {
	if capability.CreatedAt.IsZero() {
		capability.CreatedAt = now
	}
	if capability.UpdatedAt.IsZero() {
		capability.UpdatedAt = now
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO worker_capabilities (
			worker_instance_id,
			language_id,
			language_version,
			max_concurrency,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (worker_instance_id, language_id, language_version) DO UPDATE
		SET max_concurrency = EXCLUDED.max_concurrency, updated_at = EXCLUDED.updated_at
	`, capability.WorkerInstanceID, capability.LanguageID, capability.LanguageVersion, capability.MaxConcurrency, capability.CreatedAt, capability.UpdatedAt)
	return err
}

func attemptNotFoundOrConflict(ctx context.Context, tx *sql.Tx, attemptID string, submissionID string) error {
	var existingSubmissionID string
	err := tx.QueryRowContext(ctx, `
		SELECT submission_id::text
		FROM attempts
		WHERE attempt_id = $1
	`, attemptID).Scan(&existingSubmissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptNotFound
	}
	if err != nil {
		return err
	}
	if existingSubmissionID != submissionID {
		return ErrAttemptConflict
	}
	return ErrAttemptNotFound
}

func (s *PostgresRunStore) getOutboxEvent(ctx context.Context, eventID string) (OutboxEventRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			event_id::text,
			aggregate_type,
			aggregate_id::text,
			run_id::text,
			COALESCE(attempt_id::text, ''),
			event_type,
			COALESCE(topic, ''),
			COALESCE(message_key, ''),
			payload_json,
			headers_json,
			status,
			publish_attempts,
			available_at,
			published_at,
			COALESCE(last_error, ''),
			created_at,
			updated_at
		FROM outbox_events
		WHERE event_id = $1
	`, eventID)
	record, err := scanOutboxEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEventRecord{}, false, nil
	}
	if err != nil {
		return OutboxEventRecord{}, false, err
	}
	return record, true, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRunRecord(row scanner) (RunRecord, error) {
	var record RunRecord
	var completedAt sql.NullTime
	err := row.Scan(
		&record.RunID,
		&record.SubmissionID,
		&record.UserID,
		&record.TaskID,
		&record.TaskVersionID,
		&record.LanguageID,
		&record.LanguageVersion,
		&record.Status,
		&record.RequestHash,
		&record.LimitsProfileID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&completedAt,
	)
	if completedAt.Valid {
		record.CompletedAt = &completedAt.Time
	}
	return record, err
}

func scanWorkerInstance(row scanner) (WorkerInstanceRecord, error) {
	var record WorkerInstanceRecord
	var stoppedAt sql.NullTime
	err := row.Scan(
		&record.WorkerInstanceID,
		&record.Hostname,
		&record.Environment,
		&record.ExecutorType,
		&record.Version,
		&record.Status,
		&record.StartedAt,
		&record.LastSeenAt,
		&stoppedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if stoppedAt.Valid {
		record.StoppedAt = &stoppedAt.Time
	}
	return record, err
}

func scanAttemptRecord(row scanner) (AttemptRecord, error) {
	var record AttemptRecord
	var completedAt sql.NullTime
	var leaseExpiresAt sql.NullTime
	var requestJSON []byte
	var resultJSON []byte
	err := row.Scan(
		&record.AttemptID,
		&record.RunID,
		&record.SubmissionID,
		&record.AttemptNo,
		&record.Status,
		&record.RequestHash,
		&requestJSON,
		&resultJSON,
		&record.WorkerInstanceID,
		&record.StartedAt,
		&completedAt,
		&leaseExpiresAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if completedAt.Valid {
		record.CompletedAt = &completedAt.Time
	}
	if leaseExpiresAt.Valid {
		record.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	record.RequestJSON = append(json.RawMessage(nil), requestJSON...)
	record.ResultJSON = append(json.RawMessage(nil), resultJSON...)
	return record, err
}

func scanOutboxEvent(row scanner) (OutboxEventRecord, error) {
	var record OutboxEventRecord
	var publishedAt sql.NullTime
	var payloadJSON []byte
	var headersJSON []byte
	err := row.Scan(
		&record.EventID,
		&record.AggregateType,
		&record.AggregateID,
		&record.RunID,
		&record.AttemptID,
		&record.EventType,
		&record.Topic,
		&record.Key,
		&payloadJSON,
		&headersJSON,
		&record.Status,
		&record.PublishAttempts,
		&record.AvailableAt,
		&publishedAt,
		&record.LastError,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if publishedAt.Valid {
		record.PublishedAt = &publishedAt.Time
	}
	record.PayloadJSON = append(json.RawMessage(nil), payloadJSON...)
	record.HeadersJSON = append(json.RawMessage(nil), headersJSON...)
	return record, err
}

func normalizeWorkerInstance(worker *WorkerInstanceRecord, now time.Time) {
	if worker.CreatedAt.IsZero() {
		worker.CreatedAt = now
	}
	if worker.UpdatedAt.IsZero() {
		worker.UpdatedAt = now
	}
	if worker.StartedAt.IsZero() {
		worker.StartedAt = worker.CreatedAt
	}
	if worker.LastSeenAt.IsZero() {
		worker.LastSeenAt = worker.UpdatedAt
	}
	if worker.Status == "" {
		worker.Status = WorkerStatusActive
	}
}

func normalizeOutboxEvent(event *OutboxEventRecord, now time.Time) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if event.UpdatedAt.IsZero() {
		event.UpdatedAt = event.CreatedAt
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = event.CreatedAt
	}
	if event.Status == "" {
		event.Status = OutboxStatusPending
	}
	if len(event.HeadersJSON) == 0 {
		event.HeadersJSON = EmptyHeadersJSON()
	}
}

func nullableJSON(value json.RawMessage) string {
	if len(value) == 0 {
		return "null"
	}
	return string(value)
}

func rollback(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

var _ RunStore = (*PostgresRunStore)(nil)
