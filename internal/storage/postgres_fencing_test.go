//go:build component

package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
)

func TestPostgresAttemptFence(t *testing.T) {
	dsn := os.Getenv("COMPONENT_TEST_DSN")
	if dsn == "" {
		t.Fatal("COMPONENT_TEST_DSN required for isolated test database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := os.ReadFile("../../migrations/001_init_runs.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Пакеты component запускаются параллельно на одной выделенной БД.
	migration, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer migration.Rollback()
	if _, err = migration.ExecContext(ctx, "SELECT pg_advisory_xact_lock(226001)"); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = migration.Commit(); err != nil {
		t.Fatal(err)
	}
	a, err := NewPostgresRunStore(ctx, config.PostgresStore{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewPostgresRunStore(ctx, config.PostgresStore{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	req := contracts.RunRequest{SubmissionID: StableUUID(t.Name(), time.Now().String()), AttemptID: StableUUID(t.Name(), time.Now().String(), "attempt"), UserID: StableUUID("user"), TaskID: StableUUID("task"), Language: contracts.LanguageSpec{ID: "python", Version: "3.12"}, Execution: &contracts.ExecutionSpec{Mode: "execution", Inputs: []string{"x"}}, CreatedAt: time.Now()}
	owned, release, err := a.AcquireAttempt(ctx, req.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, err = b.AcquireAttempt(ctx, req.AttemptID); !errors.Is(err, ErrAttemptInProgress) {
		t.Fatalf("concurrent claim: %v", err)
	}
	if _, _, err = b.AcquireAttempt(ctx, strings.ToUpper(req.AttemptID)); !errors.Is(err, ErrAttemptInProgress) {
		t.Fatalf("case bypassed fence: %v", err)
	}
	r, err := a.ReserveAttempt(owned, req, "worker-a")
	if err != nil || r.State != AttemptReserved {
		t.Fatalf("reserve: %+v %v", r, err)
	}
	// Сервер разрывает session владельца, как при потере worker.
	var pid int
	if err = owned.Value(attemptConnectionKey{}).(attemptConnection).conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "SELECT pg_terminate_backend($1, 5000)", pid); err != nil {
		t.Fatal(err)
	}
	recovered, releaseB, err := b.AcquireAttempt(ctx, req.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	r, err = b.ReserveAttempt(recovered, req, "worker-b")
	if err != nil || r.State != AttemptDuplicateRunning {
		t.Fatalf("orphan: %+v %v", r, err)
	}
	terminal := contracts.RunResult{SchemaVersion: contracts.RunResultSchemaVersion, SubmissionID: req.SubmissionID, AttemptID: req.AttemptID, Status: contracts.StatusInternalError, WorkerID: "worker-b", CreatedAt: time.Now()}
	if err = b.CompleteAttempt(recovered, terminal); err != nil {
		t.Fatal(err)
	}
	if err = a.CompleteAttempt(owned, terminal); err == nil {
		t.Fatal("old owner wrote after losing connection")
	}
	terminal.Status = contracts.StatusOK
	if err = b.CompleteAttempt(recovered, terminal); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("terminal overwritten: %v", err)
	}
	r, err = b.ReserveAttempt(recovered, req, "worker-b")
	if err != nil || r.State != AttemptDuplicateCompleted || r.Result.Status != contracts.StatusInternalError {
		t.Fatalf("replay: %+v %v", r, err)
	}
	req.Execution.Inputs = []string{"changed"}
	if _, err = b.ReserveAttempt(recovered, req, "worker-b"); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("changed request accepted: %v", err)
	}
}
