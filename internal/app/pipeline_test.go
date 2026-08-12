package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
	"time"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/observability"
)

func TestWorkerPipelineKafkaRequestToFakeResult(t *testing.T) {
	cfg := config.Default()
	cfg.App.WorkerID = "pipeline-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Executor.Type = "fake"
	cfg.Executor.Fake.DefaultStatus = "passed"
	cfg.Executor.Fake.LatencyMS = 0
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	req := validPipelineRunRequest()
	req.Tests.Files[0].ContentB64 = base64.StdEncoding.EncodeToString([]byte("# fake_status=fail\n"))
	payload, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &pipelineConsumer{
		messages: []kafka.Message{
			{
				Topic:     cfg.Kafka.Topics.Requests,
				Partition: 0,
				Offset:    1,
				Key:       []byte(req.AttemptID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &pipelineProducer{}

	var logs bytes.Buffer
	logger := observability.NewLogger("debug", "json", &logs)
	worker, err := app.NewWorkerWithDeps(cfg, logger, app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
	})
	if err != nil {
		t.Errorf("new worker with deps: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Errorf("start worker: %v", err)
		return
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Shutdown(shutdownCtx)
	})

	if err := worker.ProcessOnce(ctx); err != nil {
		t.Errorf("process once: %v", err)
		return
	}

	if len(consumer.committed) != 1 {
		t.Errorf("expected request commit, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one result message, got %d", len(producer.messages))
		return
	}

	resultMessage := producer.messages[0]
	if resultMessage.Topic != cfg.Kafka.Topics.Results {
		t.Errorf("expected result topic %q, got %q", cfg.Kafka.Topics.Results, resultMessage.Topic)
	}
	if string(resultMessage.Key) != req.AttemptID {
		t.Errorf("expected result key %q, got %q", req.AttemptID, string(resultMessage.Key))
	}

	result, err := kafka.DecodeRunResult(resultMessage.Value)
	if err != nil {
		t.Errorf("decode result: %v", err)
		return
	}
	if result.Status != contracts.StatusFailed {
		t.Errorf("expected failed result, got %q", result.Status)
	}
	if result.Summary.TestsFailed != 1 {
		t.Errorf("expected one failed test, got %#v", result.Summary)
	}
	if result.TraceID != req.TraceID {
		t.Errorf("expected trace id %q, got %q", req.TraceID, result.TraceID)
	}
	if result.WorkerID != cfg.App.WorkerID {
		t.Errorf("expected worker id %q, got %q", cfg.App.WorkerID, result.WorkerID)
	}
}

func TestWorkerPipelineUnsupportedLanguageResult(t *testing.T) {
	cfg := config.Default()
	cfg.App.WorkerID = "pipeline-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Executor.Type = "fake"
	cfg.Executor.Fake.LatencyMS = 0
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	req := validPipelineRunRequest()
	req.Language = contracts.LanguageSpec{ID: "javascript", Version: "22"}
	payload, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &pipelineConsumer{
		messages: []kafka.Message{
			{
				Topic:     cfg.Kafka.Topics.Requests,
				Partition: 0,
				Offset:    1,
				Key:       []byte(req.AttemptID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &pipelineProducer{}

	var logs bytes.Buffer
	logger := observability.NewLogger("debug", "json", &logs)
	worker, err := app.NewWorkerWithDeps(cfg, logger, app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
	})
	if err != nil {
		t.Errorf("new worker with deps: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Errorf("start worker: %v", err)
		return
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Shutdown(shutdownCtx)
	})

	if err := worker.ProcessOnce(ctx); err != nil {
		t.Errorf("process once: %v", err)
		return
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one result message, got %d", len(producer.messages))
		return
	}

	result, err := kafka.DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode result: %v", err)
		return
	}
	if result.Status != contracts.StatusUnsupportedLanguage {
		t.Errorf("expected unsupported_language result, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Code != "unsupported_language" {
		t.Errorf("expected unsupported_language error code, got %#v", result.Error)
	}
}

type pipelineConsumer struct {
	messages  []kafka.Message
	committed []kafka.Message
}

func (c *pipelineConsumer) FetchMessage(context.Context) (kafka.Message, error) {
	if len(c.messages) == 0 {
		return kafka.Message{}, context.Canceled
	}
	msg := c.messages[0]
	c.messages = c.messages[1:]
	return msg, nil
}

func (c *pipelineConsumer) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	c.committed = append(c.committed, messages...)
	return nil
}

func (c *pipelineConsumer) Close() error {
	return nil
}

type pipelineProducer struct {
	messages []kafka.Message
}

func (p *pipelineProducer) WriteMessages(_ context.Context, messages ...kafka.Message) error {
	p.messages = append(p.messages, messages...)
	return nil
}

func (p *pipelineProducer) Close() error {
	return nil
}

func validPipelineRunRequest() contracts.RunRequest {
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
		Limits: contracts.Limits{
			CPUms:          2000,
			Wallms:         5000,
			MemoryMB:       128,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
		TraceID:   "trace-pipeline",
		CreatedAt: time.Now().UTC(),
	}
}

var _ kafka.Consumer = (*pipelineConsumer)(nil)
var _ kafka.Producer = (*pipelineProducer)(nil)
