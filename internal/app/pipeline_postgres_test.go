//go:build component

package app_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/storage"
)

type crashProducer struct {
	pipelineProducer
	fail bool
}

func (p *crashProducer) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	if p.fail {
		return errors.New("publication unavailable")
	}
	return p.pipelineProducer.WriteMessages(ctx, msgs...)
}

type crashConsumer struct {
	pipelineConsumer
	fail bool
}

func (c *crashConsumer) CommitMessages(ctx context.Context, msgs ...kafka.Message) error {
	if c.fail {
		return errors.New("commit unavailable")
	}
	return c.pipelineConsumer.CommitMessages(ctx, msgs...)
}

func TestPostgresPipelineCrashMatrix(t *testing.T) {
	dsn := os.Getenv("COMPONENT_TEST_DSN")
	if dsn == "" {
		t.Fatal("isolated COMPONENT_TEST_DSN required")
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
	for _, stage := range []string{"before_claim", "after_claim", "after_run_before_publish", "after_publish_before_commit"} {
		t.Run(stage, func(t *testing.T) {
			req := validPipelineRunRequest()
			req.SubmissionID = storage.StableUUID(t.Name(), time.Now().String())
			req.AttemptID = storage.StableUUID(req.SubmissionID, "attempt")
			store, err := storage.NewPostgresRunStore(ctx, config.PostgresStore{DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if stage == "after_claim" {
				owned, release, err := store.AcquireAttempt(ctx, req.AttemptID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.ReserveAttempt(owned, req, "crashed-worker")
				release()
				if err != nil {
					t.Fatal(err)
				}
			}
			payload, _ := kafka.EncodeRunRequest(req)
			consumer := &crashConsumer{pipelineConsumer: pipelineConsumer{messages: []kafka.Message{{Topic: "code-execution.requests.v1", Key: []byte(req.SubmissionID), Value: payload, Offset: 1}}}, fail: stage == "after_publish_before_commit"}
			producer := &crashProducer{fail: stage == "after_run_before_publish"}
			executor := &countingExecutor{}
			cfg := config.Default()
			cfg.FeatureFlags.LegacyHarness = true
			cfg.Server.HealthAddr = "127.0.0.1:0"
			cfg.Observability.MetricsEnabled = false
			cfg.App.WorkerID = "recovery-" + stage
			worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("error", "json", nil), app.WorkerDeps{Consumer: consumer, Producer: producer, RunStore: store, Executor: executor})
			if err != nil {
				t.Fatal(err)
			}
			if err = worker.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer func() {
				shutdown, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				_ = worker.Shutdown(shutdown)
			}()
			err = worker.ProcessOnce(ctx)
			if producer.fail || consumer.fail {
				if err == nil || len(consumer.committed) != 0 {
					t.Fatalf("failure committed: %v", err)
				}
				producer.fail = false
				consumer.fail = false
				if err = worker.ProcessOnce(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if stage == "after_claim" {
				expected = 0
			}
			if executor.Calls() != expected || len(consumer.committed) != 1 {
				t.Fatalf("calls=%d commits=%d", executor.Calls(), len(consumer.committed))
			}
			known, ok, err := store.GetAttemptResult(ctx, req.AttemptID)
			if err != nil || !ok {
				t.Fatalf("missing durable terminal: %v", err)
			}
			if stage == "after_claim" && (known.Status != contracts.StatusInternalError || known.Error.Code != "execution_interrupted") {
				t.Fatalf("wrong orphan result: %+v", known)
			}
			// Новое подключение читает тот же terminal после рестарта.
			restarted, err := storage.NewPostgresRunStore(ctx, config.PostgresStore{DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			owned, release, err := restarted.AcquireAttempt(ctx, req.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			replay, err := restarted.ReserveAttempt(owned, req, "restarted")
			if err != nil || replay.State != storage.AttemptDuplicateCompleted || replay.Result.Status != known.Status {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			if len(producer.messages) == 0 {
				t.Fatal("no result published")
			}
			release()
			before := len(producer.messages)
			req.Limits.CPUms++
			conflictPayload, _ := kafka.EncodeRunRequest(req)
			consumer.messages = append(consumer.messages, kafka.Message{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: conflictPayload, Offset: 2})
			if err = worker.ProcessOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if len(producer.messages) != before+1 || producer.messages[before].Topic != cfg.Kafka.Topics.DeadLetter {
				t.Fatal("conflicting request published a result")
			}
			after, ok, err := store.GetAttemptResult(ctx, req.AttemptID)
			if err != nil || !ok || after.Status != known.Status || !after.CreatedAt.Equal(known.CreatedAt) {
				t.Fatal("conflict changed terminal")
			}
			secondConsumer := &pipelineConsumer{messages: []kafka.Message{{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: payload, Offset: 1}}}
			secondProducer := &pipelineProducer{}
			cfg.App.WorkerID += "-restarted"
			second, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("error", "json", nil), app.WorkerDeps{Consumer: secondConsumer, Producer: secondProducer, RunStore: restarted, Executor: executor})
			if err != nil {
				t.Fatal(err)
			}
			if err = second.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer func() {
				shutdown, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				_ = second.Shutdown(shutdown)
			}()
			if err = second.ProcessOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if executor.Calls() != expected || len(secondConsumer.committed) != 1 || len(secondProducer.messages) != 1 {
				t.Fatal("restarted worker executed or lost result")
			}
			replayed, err := kafka.DecodeRunResult(secondProducer.messages[0].Value)
			if err != nil || replayed.Status != known.Status || !replayed.CreatedAt.Equal(known.CreatedAt) {
				t.Fatal("restarted worker did not replay exact terminal")
			}

		})
	}
}
