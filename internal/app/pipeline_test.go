package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/artifacts"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/objectstore"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/storage"
	"overmindv/sandbox/internal/testsources"
)

func TestWorkerPipelineKafkaRequestToFakeResult(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
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
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
				Time:      req.CreatedAt.Add(-2 * time.Second),
			},
		},
	}
	producer := &pipelineProducer{}
	store := storage.NewMemoryRunStore()
	metrics := observability.NewSchedulerMetrics()

	var logs bytes.Buffer
	logger := observability.NewLogger("debug", "json", &logs)
	worker, err := app.NewWorkerWithDeps(cfg, logger, app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
		RunStore: store,
		Metrics:  metrics,
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
	if string(resultMessage.Key) != req.SubmissionID {
		t.Errorf("expected result key %q, got %q", req.SubmissionID, string(resultMessage.Key))
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

	run, ok, err := store.GetRunBySubmission(context.Background(), req.SubmissionID)
	if err != nil {
		t.Errorf("get stored run: %v", err)
		return
	}
	if !ok {
		t.Error("expected run to be stored")
		return
	}
	outboxEvents, err := store.ListOutboxEvents(context.Background(), storage.OutboxFilter{Status: storage.OutboxStatusPending})
	if err != nil {
		t.Errorf("list outbox events: %v", err)
		return
	}
	if len(outboxEvents) != 1 {
		t.Errorf("expected one pending outbox event, got %d", len(outboxEvents))
		return
	}
	if outboxEvents[0].RunID != run.RunID || outboxEvents[0].AttemptID != req.AttemptID {
		t.Errorf("expected outbox to reference run %q attempt %q, got %#v", run.RunID, req.AttemptID, outboxEvents[0])
	}

	snapshot := metrics.Snapshot()
	if snapshot.Consumed[cfg.Kafka.Topics.Requests] != 1 {
		t.Errorf("expected consumed request metric, got %#v", snapshot.Consumed)
	}
	if snapshot.Produced[cfg.Kafka.Topics.Results] != 1 {
		t.Errorf("expected produced result metric, got %#v", snapshot.Produced)
	}
	if snapshot.ResultStatuses[string(contracts.StatusFailed)] != 1 {
		t.Errorf("expected failed result metric, got %#v", snapshot.ResultStatuses)
	}
	if snapshot.ResultLanguages["python"] != 1 {
		t.Errorf("expected python result metric, got %#v", snapshot.ResultLanguages)
	}
	if snapshot.ExecutorLatencyByLanguage["python"].Count != 1 {
		t.Errorf("expected executor latency metric, got %#v", snapshot.ExecutorLatencyByLanguage)
	}
	if snapshot.QueueLagByTopic[cfg.Kafka.Topics.Requests].Count != 1 {
		t.Errorf("expected queue lag metric, got %#v", snapshot.QueueLagByTopic)
	}
}

func TestWorkerPipelineExecutionModeToFakeResult(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-exec-worker"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Executor.Fake.DefaultStatus = "passed"
	cfg.Executor.Fake.LatencyMS = 0
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	req := validPipelineExecutionRunRequest()
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
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
				Time:      req.CreatedAt.Add(-2 * time.Second),
			},
		},
	}
	producer := &pipelineProducer{}
	store := storage.NewMemoryRunStore()
	metrics := observability.NewSchedulerMetrics()

	var logs bytes.Buffer
	logger := observability.NewLogger("debug", "json", &logs)
	worker, err := app.NewWorkerWithDeps(cfg, logger, app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
		RunStore: store,
		Metrics:  metrics,
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
	if result.Status != contracts.StatusOK {
		t.Errorf("expected ok status, got %q", result.Status)
	}
	if result.Summary.CasesTotal != 2 {
		t.Errorf("expected cases_total 2, got %d", result.Summary.CasesTotal)
	}
	if result.Summary.CasesWithError != 0 {
		t.Errorf("expected cases_with_error 0, got %d", result.Summary.CasesWithError)
	}
	if len(result.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(result.Cases))
	}
	if result.Cases[0].Stdout != "1" || result.Cases[1].Stdout != "2" {
		t.Errorf("expected case stdout echo of inputs, got %q and %q", result.Cases[0].Stdout, result.Cases[1].Stdout)
	}
	if result.Cases[0].Status != string(contracts.StatusOK) || result.Cases[1].Status != string(contracts.StatusOK) {
		t.Errorf("expected case status ok, got %q and %q", result.Cases[0].Status, result.Cases[1].Status)
	}
}

func TestWorkerPipelineSharedExecutionFixtures(t *testing.T) {
	for _, mode := range []string{"execution", "execution-go"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.Default()
			cfg.FeatureFlags.LegacyHarness = true
			cfg.App.WorkerID = "pipeline-exec-worker"
			cfg.Server.HealthAddr = "127.0.0.1:0"
			cfg.Observability.MetricsEnabled = false
			cfg.Executor.Type = "fake"
			cfg.Executor.Fake.DefaultStatus = "passed"
			cfg.Executor.Fake.LatencyMS = 0
			cfg.Scheduler.JobTimeoutMS = 1000
			cfg.Scheduler.Queue.MaxDepth = 8
			cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

			payload, err := os.ReadFile("../../tests/fixtures/execution-contract/" + mode + ".request.json")
			if err != nil {
				t.Fatal(err)
			}
			req, err := kafka.DecodeRunRequest(payload)
			if err != nil {
				t.Fatal(err)
			}
			// Golden fixture имеет фиксированную дату; проверяем свежую доставку.
			req.CreatedAt = time.Now().UTC()
			payload, err = kafka.EncodeRunRequest(req)
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
						Key:       []byte(req.SubmissionID),
						Value:     payload,
						Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
						Time:      req.CreatedAt.Add(-2 * time.Second),
					},
				},
			}
			producer := &pipelineProducer{}
			store := storage.NewMemoryRunStore()
			metrics := observability.NewSchedulerMetrics()

			var logs bytes.Buffer
			logger := observability.NewLogger("debug", "json", &logs)
			worker, err := app.NewWorkerWithDeps(cfg, logger, app.WorkerDeps{
				Consumer: consumer,
				Producer: producer,
				RunStore: store,
				Metrics:  metrics,
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
				t.Errorf("decode result: %v; logs: %s", err, logs.String())
				return
			}
			if result.Status != contracts.StatusOK {
				t.Errorf("expected ok status, got %q", result.Status)
			}

			if len(consumer.committed) != 1 {
				t.Fatal("request not committed")
			}
			if string(producer.messages[0].Key) != req.SubmissionID || result.AttemptID != req.AttemptID {
				t.Fatal("identity mismatch")
			}
			if result.Summary.CasesTotal != len(req.Execution.Inputs) || len(result.Cases) != len(req.Execution.Inputs) {
				t.Fatal("incomplete cases")
			}
			for i, c := range result.Cases {
				if c.Index != i || c.Stdout != req.Execution.Inputs[i] || c.Status != string(contracts.StatusOK) {
					t.Fatalf("invalid case %d: %+v", i, c)
				}
			}
			expected := contracts.EvaluationContextForRequest(req)
			if expected == nil || result.EvaluationContext == nil || *expected != *result.EvaluationContext {
				t.Fatal("evaluation context mismatch")
			}
		})
	}
}

func TestWorkerPipelineKafkaGoRequestToFakeResult(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-go-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Executor.Fake.DefaultStatus = "passed"
	cfg.Executor.Fake.LatencyMS = 0
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	req := validPipelineGoRunRequest()
	payload, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode Go request: %v", err)
		return
	}

	consumer := &pipelineConsumer{
		messages: []kafka.Message{
			{
				Topic:     cfg.Kafka.Topics.Requests,
				Partition: 0,
				Offset:    20,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
				Time:      req.CreatedAt.Add(-2 * time.Second),
			},
		},
	}
	producer := &pipelineProducer{}
	store := storage.NewMemoryRunStore()
	metrics := observability.NewSchedulerMetrics()

	var logs bytes.Buffer
	logger := observability.NewLogger("debug", "json", &logs)
	worker, err := app.NewWorkerWithDeps(cfg, logger, app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
		RunStore: store,
		Metrics:  metrics,
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
		t.Errorf("process Go request once: %v", err)
		return
	}

	if len(consumer.committed) != 1 {
		t.Errorf("expected Go request commit, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one Go result message, got %d", len(producer.messages))
		return
	}

	result, err := kafka.DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode Go result: %v", err)
		return
	}
	if result.Status != contracts.StatusPassed {
		t.Errorf("expected Go result passed, got %q", result.Status)
	}
	if result.Language.ID != "go" {
		t.Errorf("expected Go result language, got %#v", result.Language)
	}

	snapshot := metrics.Snapshot()
	if snapshot.ResultLanguages["go"] != 1 {
		t.Errorf("expected Go result metric, got %#v", snapshot.ResultLanguages)
	}
	if snapshot.ExecutorLatencyByLanguage["go"].Count != 1 {
		t.Errorf("expected Go executor latency metric, got %#v", snapshot.ExecutorLatencyByLanguage)
	}
}

func TestWorkerPipelineStoresLargeStdoutArtifact(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-artifact-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10
	cfg.Storage.Artifacts.Enabled = true
	cfg.Storage.Artifacts.Provider = "filesystem"
	cfg.Storage.Artifacts.Bucket = "local"
	cfg.Storage.Artifacts.KeyPrefix = "test-artifacts"
	cfg.Storage.Artifacts.InlineMaxBytes = 8
	cfg.Storage.Artifacts.MaxBytes = 1024

	req := validPipelineRunRequest()
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
				Offset:    2,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &pipelineProducer{}
	store := storage.NewMemoryRunStore()
	metrics := observability.NewSchedulerMetrics()
	objectStore, err := objectstore.NewFilesystemStore(objectstore.FilesystemConfig{
		RootDir: t.TempDir(),
		Bucket:  "local",
	})
	if err != nil {
		t.Errorf("create filesystem object store: %v", err)
		return
	}
	artifactManager, err := artifacts.NewManager(cfg.Storage.Artifacts, objectStore)
	if err != nil {
		t.Errorf("create artifact manager: %v", err)
		return
	}

	worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("debug", "json", nil), app.WorkerDeps{
		Consumer:  consumer,
		Producer:  producer,
		RunStore:  store,
		Executor:  &stdoutExecutor{stdout: strings.Repeat("x", 64)},
		Metrics:   metrics,
		Artifacts: artifactManager,
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
	if result.Logs.Stdout != strings.Repeat("x", 8) {
		t.Errorf("expected stdout preview, got %q", result.Logs.Stdout)
	}
	if !result.Logs.InlineTruncated {
		t.Error("expected inline truncated flag")
	}
	if result.Logs.StdoutArtifact == nil {
		t.Error("expected stdout artifact reference")
		return
	}

	artifactBody, err := objectStore.Get(context.Background(), result.Logs.StdoutArtifact.Bucket, result.Logs.StdoutArtifact.Key)
	if err != nil {
		t.Errorf("get stdout artifact: %v", err)
		return
	}
	if string(artifactBody) != strings.Repeat("x", 64) {
		t.Errorf("expected full stdout artifact body, got %q", string(artifactBody))
	}

	attempt, ok, err := store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected stored attempt")
		return
	}
	artifactRows, err := store.ListArtifacts(context.Background(), attempt.RunID, req.AttemptID)
	if err != nil {
		t.Errorf("list artifacts: %v", err)
		return
	}
	if len(artifactRows) != 1 {
		t.Errorf("expected one artifact row, got %d", len(artifactRows))
		return
	}
	if artifactRows[0].ObjectKey != result.Logs.StdoutArtifact.Key {
		t.Errorf("expected artifact row key %q, got %q", result.Logs.StdoutArtifact.Key, artifactRows[0].ObjectKey)
	}
	if artifactRows[0].StorageProvider != storage.ArtifactStorageFilesystem {
		t.Errorf("expected filesystem artifact provider, got %q", artifactRows[0].StorageProvider)
	}
	snapshot := metrics.Snapshot()
	if snapshot.ArtifactsStored["stdout"] != 1 {
		t.Errorf("expected stdout artifact metric, got %#v", snapshot.ArtifactsStored)
	}
	if snapshot.ArtifactBytes["stdout"] != 64 {
		t.Errorf("expected stdout artifact bytes metric, got %#v", snapshot.ArtifactBytes)
	}
}

func TestWorkerPipelineResolvesObjectStorageTests(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-test-source-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	testSources, suiteChecksum := objectStorageTestProvider(t)
	req := validPipelineRunRequest()
	req.Tests.Source = contracts.TestSourceObjectStorage
	req.Tests.SuiteID = "suite-1"
	req.Tests.Version = "v1"
	req.Tests.ChecksumSHA256 = suiteChecksum
	req.Tests.Entrypoint = ""
	req.Tests.Files = nil
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
				Offset:    3,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &pipelineProducer{}
	store := storage.NewMemoryRunStore()

	worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("debug", "json", nil), app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
		RunStore: store,
		Executor: &countingExecutor{},
		Tests:    testSources,
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
	if result.Status != contracts.StatusPassed {
		t.Errorf("expected passed result, got %q", result.Status)
	}
	run, ok, err := store.GetRunBySubmission(context.Background(), req.SubmissionID)
	if err != nil {
		t.Errorf("get run: %v", err)
		return
	}
	if !ok {
		t.Error("expected run")
		return
	}
	tests, err := store.ListRunTests(context.Background(), run.RunID)
	if err != nil {
		t.Errorf("list run tests: %v", err)
		return
	}
	if len(tests) != 1 || tests[0].Source != string(contracts.TestSourceObjectStorage) {
		t.Errorf("expected original object storage test snapshot, got %#v", tests)
	}
}

func TestWorkerPipelineUnsupportedLanguageResult(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
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
				Key:       []byte(req.SubmissionID),
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

func TestWorkerPipelineDuplicateAttemptRepublishesKnownResult(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-idempotency-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	req := validPipelineRunRequest()
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
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
			{
				Topic:     cfg.Kafka.Topics.Requests,
				Partition: 0,
				Offset:    2,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &pipelineProducer{}
	executor := &countingExecutor{}

	worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("debug", "json", nil), app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
		Executor: executor,
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
		t.Errorf("process first message: %v", err)
		return
	}
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Errorf("process duplicate message: %v", err)
		return
	}

	if executor.Calls() != 1 {
		t.Errorf("expected executor to run once, got %d calls", executor.Calls())
	}
	if len(consumer.committed) != 2 {
		t.Errorf("expected two committed messages, got %d", len(consumer.committed))
	}

	messages := producer.Messages()
	if len(messages) != 2 {
		t.Errorf("expected two result messages, got %d", len(messages))
		return
	}

	first, err := kafka.DecodeRunResult(messages[0].Value)
	if err != nil {
		t.Errorf("decode first result: %v", err)
		return
	}
	second, err := kafka.DecodeRunResult(messages[1].Value)
	if err != nil {
		t.Errorf("decode duplicate result: %v", err)
		return
	}
	if first.AttemptID != req.AttemptID || second.AttemptID != req.AttemptID {
		t.Errorf("expected both results for attempt %q, got %q and %q", req.AttemptID, first.AttemptID, second.AttemptID)
	}
	if first.Status != contracts.StatusPassed || second.Status != contracts.StatusPassed {
		t.Errorf("expected passed results, got %q and %q", first.Status, second.Status)
	}
	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Errorf("expected duplicate to republish stored result created_at, got %s and %s", first.CreatedAt, second.CreatedAt)
	}
}

func TestWorkerPipelineRetryableExecutorErrorSchedulesRetry(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-retry-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10
	cfg.Kafka.Retry.Enabled = true
	cfg.Kafka.Retry.MaxAttempts = 3
	cfg.Kafka.Retry.InitialBackoffMS = 0
	cfg.Kafka.Retry.MaxBackoffMS = 0
	cfg.Kafka.Retry.BackoffMultiplier = 1

	req := validPipelineRunRequest()
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
				Offset:    10,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &pipelineProducer{}
	store := storage.NewMemoryRunStore()

	worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("debug", "json", nil), app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
		Executor: &failingExecutor{err: errors.New("go-judge unavailable")},
		RunStore: store,
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
		t.Errorf("process retryable message: %v", err)
		return
	}
	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}

	messages := producer.Messages()
	if len(messages) != 1 {
		t.Errorf("expected one retry message, got %d", len(messages))
		return
	}
	if messages[0].Topic != cfg.Kafka.Topics.Retry {
		t.Errorf("expected retry topic %q, got %q", cfg.Kafka.Topics.Retry, messages[0].Topic)
	}
	if messages[0].Headers[kafka.HeaderRetryCount] != "1" {
		t.Errorf("expected retry count 1, got %q", messages[0].Headers[kafka.HeaderRetryCount])
	}

	attempt, ok, err := store.GetAttempt(context.Background(), req.AttemptID)
	if err != nil {
		t.Errorf("get attempt: %v", err)
		return
	}
	if !ok {
		t.Error("expected stored attempt")
		return
	}
	if attempt.Status != storage.AttemptStatusFailedInternal {
		t.Errorf("expected failed_internal attempt, got %q", attempt.Status)
	}
	if attempt.LastErrorReason != "execution_infra_error" {
		t.Errorf("expected execution_infra_error reason, got %#v", attempt)
	}

	outboxEvents, err := store.ListOutboxEvents(context.Background(), storage.OutboxFilter{Status: storage.OutboxStatusPending})
	if err != nil {
		t.Errorf("list outbox events: %v", err)
		return
	}
	if len(outboxEvents) != 1 {
		t.Errorf("expected one retry outbox event, got %d", len(outboxEvents))
		return
	}
	if outboxEvents[0].EventType != storage.OutboxEventRetryReady {
		t.Errorf("expected retry outbox event, got %q", outboxEvents[0].EventType)
	}
}

func TestWorkerRunConsumesKafkaUntilShutdown(t *testing.T) {
	cfg := config.Default()
	cfg.FeatureFlags.LegacyHarness = true
	cfg.App.WorkerID = "pipeline-run-worker-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.Executor.Type = "fake"
	cfg.Executor.Fake.DefaultStatus = "passed"
	cfg.Executor.Fake.LatencyMS = 0
	cfg.Scheduler.JobTimeoutMS = 1000
	cfg.Scheduler.Queue.MaxDepth = 8
	cfg.Scheduler.Queue.EnqueueTimeoutMS = 10

	req := validPipelineRunRequest()
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
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{kafka.HeaderTraceID: req.TraceID},
			},
		},
	}
	resultWritten := make(chan struct{}, 1)
	producer := &pipelineProducer{
		onWrite: func() {
			select {
			case resultWritten <- struct{}{}:
			default:
			}
		},
	}

	worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("debug", "json", nil), app.WorkerDeps{
		Consumer: consumer,
		Producer: producer,
	})
	if err != nil {
		t.Errorf("new worker with deps: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	select {
	case <-resultWritten:
	case <-time.After(time.Second):
		t.Error("expected worker run loop to publish result")
		cancel()
		return
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("run worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("expected worker run loop to stop after cancel")
	}

	messages := producer.Messages()
	if len(messages) != 1 {
		t.Errorf("expected one result message, got %d", len(messages))
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
	mu       sync.Mutex
	messages []kafka.Message
	onWrite  func()
}

func (p *pipelineProducer) WriteMessages(_ context.Context, messages ...kafka.Message) error {
	p.mu.Lock()
	p.messages = append(p.messages, messages...)
	p.mu.Unlock()
	if p.onWrite != nil {
		p.onWrite()
	}
	return nil
}

func (p *pipelineProducer) Close() error {
	return nil
}

func (p *pipelineProducer) Messages() []kafka.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	messages := make([]kafka.Message, len(p.messages))
	copy(messages, p.messages)
	return messages
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

func validPipelineExecutionRunRequest() contracts.RunRequest {
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
					ContentB64: base64.StdEncoding.EncodeToString([]byte("print(input())\n")),
				},
			},
		},
		Execution: &contracts.ExecutionSpec{
			Mode:   contracts.ExecutionMode,
			Inputs: []string{"1", "2"},
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
		TraceID:   "trace-pipeline-exec",
		CreatedAt: time.Now().UTC(),
	}
}

func validPipelineGoRunRequest() contracts.RunRequest {
	req := validPipelineRunRequest()
	req.SubmissionID = "55555555-5555-4555-8555-555555555555"
	req.AttemptID = "66666666-6666-4666-8666-666666666666"
	req.Language = contracts.LanguageSpec{ID: "go", Version: "1.26"}
	req.Code = contracts.CodeSpec{
		Entrypoint: "solution.go",
		Files: []contracts.SourceFile{
			{
				Path:       "solution.go",
				ContentB64: base64.StdEncoding.EncodeToString([]byte("package solution\n\nfunc Add(a, b int) int { return a + b }\n")),
			},
		},
	}
	req.Tests = contracts.TestSpec{
		Mode:       contracts.TestModeGoTest,
		Source:     contracts.TestSourceInline,
		Entrypoint: "solution_test.go",
		Files: []contracts.SourceFile{
			{
				Path:       "solution_test.go",
				ContentB64: base64.StdEncoding.EncodeToString([]byte("package solution\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(1, 2); got != 3 {\n\t\tt.Errorf(\"Add() = %d\", got)\n\t}\n}\n")),
			},
		},
	}
	req.TraceID = "trace-pipeline-go"
	req.CreatedAt = time.Now().UTC()
	return req
}

func objectStorageTestProvider(t *testing.T) (testsources.TestSourceProvider, string) {
	t.Helper()

	content := []byte("from solution import add\n\ndef test_add():\n    assert add(1, 2) == 3\n")
	suite := testsources.Suite{
		SchemaVersion: testsources.SuiteSchemaVersion,
		SuiteID:       "suite-1",
		Version:       "v1",
		Mode:          contracts.TestModePytest,
		Entrypoint:    "test_solution.py",
		Files: []contracts.SourceFile{
			{
				Path:           "test_solution.py",
				ContentB64:     base64.StdEncoding.EncodeToString(content),
				ChecksumSHA256: testsources.SHA256Hex(content),
			},
		},
	}
	payload, err := json.Marshal(suite)
	if err != nil {
		t.Errorf("marshal suite: %v", err)
		return testsources.InlineProvider{}, ""
	}
	store := objectstore.NewMemoryStore("tests")
	key, err := testsources.SuiteKey("sandbox-tests", suite.SuiteID, suite.Version)
	if err != nil {
		t.Errorf("build suite key: %v", err)
		return testsources.InlineProvider{}, ""
	}
	if _, err := store.Put(context.Background(), objectstore.Object{
		Bucket:      "tests",
		Key:         key,
		ContentType: "application/json",
		Content:     payload,
	}); err != nil {
		t.Errorf("put suite: %v", err)
		return testsources.InlineProvider{}, ""
	}
	return testsources.NewResolver(
		testsources.NewObjectStorageProvider(config.ObjectStorageTestsConfig{
			Enabled:   true,
			Provider:  "s3",
			Bucket:    "tests",
			KeyPrefix: "sandbox-tests",
			MaxBytes:  1024,
		}, store),
	), testsources.SHA256Hex(payload)
}

var _ kafka.Consumer = (*pipelineConsumer)(nil)
var _ kafka.Producer = (*pipelineProducer)(nil)

type countingExecutor struct {
	mu    sync.Mutex
	calls int
}

func (e *countingExecutor) Execute(context.Context, execution.ExecutionCommand) (execution.ExecutionResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()

	exitCode := 0
	return execution.ExecutionResult{
		Status:          contracts.StatusPassed,
		TestsTotal:      1,
		TestsPassed:     1,
		TestCases:       []contracts.TestCaseResult{{Name: "counting::passed", Status: "passed"}},
		Stdout:          "counting executor: passed\n",
		CPUms:           1,
		MemoryPeakBytes: 1024,
		ExitCode:        &exitCode,
		RunMS:           1,
	}, nil
}

func (e *countingExecutor) Healthy() bool {
	return true
}

func (e *countingExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

var _ execution.Executor = (*countingExecutor)(nil)

type stdoutExecutor struct {
	stdout string
}

func (e *stdoutExecutor) Execute(context.Context, execution.ExecutionCommand) (execution.ExecutionResult, error) {
	exitCode := 0
	return execution.ExecutionResult{
		Status:          contracts.StatusPassed,
		TestsTotal:      1,
		TestsPassed:     1,
		TestCases:       []contracts.TestCaseResult{{Name: "stdout::passed", Status: "passed"}},
		Stdout:          e.stdout,
		CPUms:           1,
		MemoryPeakBytes: 1024,
		ExitCode:        &exitCode,
		RunMS:           1,
	}, nil
}

func (e *stdoutExecutor) Healthy() bool {
	return true
}

var _ execution.Executor = (*stdoutExecutor)(nil)

type failingExecutor struct {
	err error
}

func (e *failingExecutor) Execute(context.Context, execution.ExecutionCommand) (execution.ExecutionResult, error) {
	return execution.ExecutionResult{}, e.err
}

func (e *failingExecutor) Healthy() bool {
	return true
}

var _ execution.Executor = (*failingExecutor)(nil)

func TestWorkerDisablesLegacyHarnessByDefault(t *testing.T) {
	for _, req := range []contracts.RunRequest{validPipelineRunRequest(), validPipelineGoRunRequest()} {
		cfg := config.Default()
		cfg.Server.HealthAddr = "127.0.0.1:0"
		cfg.Observability.MetricsEnabled = false
		payload, _ := kafka.EncodeRunRequest(req)
		consumer := &pipelineConsumer{messages: []kafka.Message{{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: payload}}}
		producer := &pipelineProducer{}
		executor := &countingExecutor{}
		worker, err := app.NewWorkerWithDeps(cfg, observability.NewLogger("error", "json", nil), app.WorkerDeps{Consumer: consumer, Producer: producer, RunStore: storage.NewMemoryRunStore(), Executor: executor})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err = worker.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if err = worker.ProcessOnce(ctx); err != nil {
			t.Fatal(err)
		}
		shutdown, cancel := context.WithTimeout(ctx, time.Second)
		_ = worker.Shutdown(shutdown)
		cancel()
		if executor.Calls() != 0 || len(producer.messages) != 1 {
			t.Fatal("legacy harness executed")
		}
		result, err := kafka.DecodeRunResult(producer.messages[0].Value)
		if err != nil || result.Status != contracts.StatusInvalidRequest || result.Error.Code != "checker_error" {
			t.Fatalf("wrong blocked result: %+v %v", result, err)
		}
	}
}
