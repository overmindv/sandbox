//go:build integration

package app_test

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/storage"
)

func TestKafkaWorkerDrain(t *testing.T) {
	broker := integrationKafkaBroker(t, "PLAINTEXT")
	host, _, err := net.SplitHostPort(broker)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("isolated loopback Kafka broker required")
	}
	dsn := os.Getenv("SANDBOX_TEST_POSTGRES_DSN")
	endpoint, err := url.Parse(dsn)
	if err != nil || endpoint.Hostname() != "127.0.0.1" || endpoint.Path != "/sandbox_test" {
		t.Fatal("isolated sandbox_test PostgreSQL database required")
	}

	for _, mode := range []string{"complete", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			prefix := "drain-" + mode + "-" + storage.StableUUID(time.Now().String(), mode)
			cfg := drainConfig()
			cfg.App.WorkerID = prefix + "-first"
			cfg.Kafka.Brokers = []string{broker}
			cfg.Kafka.GroupID = prefix
			cfg.Kafka.Consumer.AutoOffsetReset = "earliest"
			cfg.Kafka.Topics = config.KafkaTopics{
				Requests: prefix + ".requests", Retry: prefix + ".retry", Events: prefix + ".events",
				Results: prefix + ".results", DeadLetter: prefix + ".deadletter",
			}
			cfg.Storage.Type = "postgres"
			cfg.Storage.Postgres.DSN = dsn
			if mode == "timeout" {
				cfg.App.ShutdownTimeoutMS = 150
			}
			createDrainTopics(t, ctx, broker, cfg.Kafka.Topics)

			input := &kafkago.Writer{Addr: kafkago.TCP(broker), RequiredAcks: kafkago.RequireAll}
			output := kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{broker}, Topic: cfg.Kafka.Topics.Results})
			defer closeKafkaFixture(t, input, output)
			first := sendDrainRequest(t, ctx, input, cfg.Kafka.Topics.Requests, prefix+"-1")
			second := sendDrainRequest(t, ctx, input, cfg.Kafka.Topics.Requests, prefix+"-2")

			executor := newDrainExecutor()
			w, closed := newLiveDrainWorker(t, cfg, executor)
			stop, done := startDrainWorker(t, w)
			waitDrainSignal(t, executor.started)
			stop()
			if mode == "complete" {
				waitNotReady(t, w)
				close(executor.release)
			}
			stopErr := waitDrainResult(t, done)
			waitDrainSignal(t, closed)

			cfg.App.WorkerID = prefix + "-next"
			recovery := &countingExecutor{}
			next, _ := newLiveDrainWorker(t, cfg, recovery)
			if err := next.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer func() {
				shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				if err := next.Shutdown(shutdown); err != nil {
					t.Error(err)
				}
			}()
			if mode == "timeout" {
				if !errors.Is(stopErr, context.DeadlineExceeded) {
					t.Fatal("expected shutdown timeout", stopErr)
				}
				waitDrainSignal(t, executor.canceled)
				if err := next.ProcessOnce(ctx); err != nil {
					t.Fatal(err)
				}
				result := readDrainResult(t, ctx, output, first)
				if result.Error == nil || result.Error.Code != "execution_interrupted" || recovery.Calls() != 0 {
					t.Fatal("uncommitted attempt was not recovered without re-executing it")
				}
			} else {
				if stopErr != nil {
					t.Fatal(stopErr)
				}
				result := readDrainResult(t, ctx, output, first)
				if result.Status != contracts.StatusPassed || result.WorkerID != prefix+"-first" {
					t.Fatal("first worker did not finish its job")
				}
			}
			if err := next.ProcessOnce(ctx); err != nil {
				t.Fatal(err)
			}
			result := readDrainResult(t, ctx, output, second)
			if result.Status != contracts.StatusPassed || result.WorkerID != cfg.App.WorkerID || recovery.Calls() != 1 {
				t.Error("next worker did not receive the second job")
			}
		})
	}
}

func createDrainTopics(t *testing.T, ctx context.Context, broker string, names config.KafkaTopics) {
	t.Helper()
	client := &kafkago.Client{Addr: kafkago.TCP(broker)}
	var topics []kafkago.TopicConfig
	for _, name := range []string{names.Requests, names.Retry, names.Events, names.Results, names.DeadLetter} {
		topics = append(topics, kafkago.TopicConfig{Topic: name, NumPartitions: 1, ReplicationFactor: 1})
	}
	resp, err := client.CreateTopics(ctx, &kafkago.CreateTopicsRequest{Topics: topics})
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range resp.Errors {
		if err != nil {
			t.Fatal(name, err)
		}
	}
}

func newLiveDrainWorker(t *testing.T, cfg config.Config, executor execution.Executor) (*app.Worker, <-chan struct{}) {
	t.Helper()
	consumer, producer, err := kafka.NewClients(cfg.Kafka)
	if err != nil {
		t.Fatal(err)
	}
	observed := &drainConsumer{Consumer: consumer, closed: make(chan struct{})}
	w, err := app.NewWorkerWithDeps(cfg, nil, app.WorkerDeps{Consumer: observed, Producer: producer, Executor: executor})
	if err != nil {
		closeKafkaFixture(t, consumer, producer)
		t.Fatal(err)
	}
	return w, observed.closed
}

func sendDrainRequest(t *testing.T, ctx context.Context, writer *kafkago.Writer, topic, name string) string {
	t.Helper()
	req := validPipelineExecutionRunRequest()
	req.SubmissionID = storage.StableUUID(name)
	req.AttemptID = storage.StableUUID(req.SubmissionID, "attempt")
	value, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteMessages(ctx, kafkago.Message{Topic: topic, Key: []byte(req.SubmissionID), Value: value}); err != nil {
		t.Fatal(err)
	}
	return req.AttemptID
}

func readDrainResult(t *testing.T, ctx context.Context, reader *kafkago.Reader, attemptID string) contracts.RunResult {
	t.Helper()
	msg, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := kafka.DecodeRunResult(msg.Value)
	if err != nil || result.AttemptID != attemptID {
		t.Fatal("unexpected result", result.AttemptID, err)
	}
	return result
}
