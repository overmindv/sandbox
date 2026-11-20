//go:build integration

package app_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/kafka"
)

func TestKafkaSecurityWorkerReconnect(t *testing.T) {
	for _, mode := range []string{"mtls", "scram"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.Default()
			cfg.App.Environment = "prod"
			cfg.App.WorkerID = "kafka-test-" + mode
			cfg.Server.HealthAddr = "127.0.0.1:0"
			cfg.Observability.MetricsEnabled = false
			cfg.Executor.Fake.LatencyMS = 0
			cfg.Kafka = integrationKafkaConfig(t, mode)
			cfg.Kafka.Topics.Requests = mode + ".requests.v1"
			cfg.Kafka.Topics.Retry = mode + ".retry.v1"
			cfg.Kafka.Topics.Events = mode + ".events.v1"
			cfg.Kafka.Topics.Results = mode + ".results.v1"
			cfg.Kafka.Topics.DeadLetter = mode + ".deadletter.v1"
			cfg.Kafka.GroupID = mode + "-worker-v1"
			cfg.Kafka.Consumer.AutoOffsetReset = "earliest"

			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			broker := integrationKafkaBroker(t, "PLAINTEXT")
			input := &kafkago.Writer{Addr: kafkago.TCP(broker), RequiredAcks: kafkago.RequireAll}
			output := kafkago.NewReader(kafkago.ReaderConfig{
				Brokers: []string{broker}, Topic: cfg.Kafka.Topics.Results, Partition: 0,
			})
			defer closeKafkaFixture(t, input, output)

			for attempt := 1; attempt <= 2; attempt++ {
				req := validPipelineRunRequest()
				req.SubmissionID = fmt.Sprintf("10000000-0000-4000-8000-%012d", attempt)
				req.AttemptID = fmt.Sprintf("20000000-0000-4000-8000-%012d", attempt)
				value, err := kafka.EncodeRunRequest(req)
				if err != nil {
					t.Fatal(err)
				}
				if err := input.WriteMessages(ctx, kafkago.Message{
					Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: value,
				}); err != nil {
					t.Fatal(err)
				}
				processKafkaFixture(t, ctx, cfg)
				message, err := output.FetchMessage(ctx)
				if err != nil {
					t.Fatal(err)
				}
				result, err := kafka.DecodeRunResult(message.Value)
				if err != nil {
					t.Fatal(err)
				}
				if result.AttemptID != req.AttemptID || string(message.Key) != req.SubmissionID || result.Error != nil {
					t.Fatal("worker restart lost or replayed a committed request")
				}
			}
		})
	}
}

func processKafkaFixture(t *testing.T, ctx context.Context, cfg config.Config) {
	t.Helper()
	worker, err := app.NewWorker(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := worker.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	}()
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestKafkaSecurityRejectsInvalidIdentity(t *testing.T) {
	secretDir := os.Getenv("SANDBOX_TEST_KAFKA_SECRET_DIR")
	for _, name := range []string{"wrong CA", "wrong host", "wrong client certificate", "wrong SCRAM password"} {
		t.Run(name, func(t *testing.T) {
			cfg := integrationKafkaConfig(t, "mtls")
			switch name {
			case "wrong CA":
				cfg.Security.CAFile = filepath.Join(secretDir, "wrong-ca.pem")
			case "wrong host":
				cfg.Brokers = []string{integrationKafkaBroker(t, "WRONG_HOST")}
			case "wrong client certificate":
				cfg.Security.CertFile = filepath.Join(secretDir, "foreign-client.pem")
				cfg.Security.KeyFile = filepath.Join(secretDir, "foreign-client.key")
			case "wrong SCRAM password":
				cfg = integrationKafkaConfig(t, "scram")
				cfg.Security.PasswordFile = filepath.Join(secretDir, "wrong-password")
			}
			reader, writer, err := kafka.NewClients(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer closeKafkaFixture(t, reader, writer)
			ctx, cancel := context.WithTimeout(t.Context(), 2200*time.Millisecond)
			defer cancel()
			if err := writer.WriteMessages(ctx, kafka.Message{Topic: "bench.v1", Value: []byte("unexpected")}); err == nil {
				t.Error("broker accepted invalid TLS or SASL identity")
			}
		})
	}
}

func BenchmarkKafkaPublishSecurity(b *testing.B) {
	for _, mode := range []string{"plaintext", "mtls", "scram"} {
		b.Run(mode, func(b *testing.B) {
			cfg := integrationKafkaConfig(b, mode)
			cfg.Producer.Compression = "none"
			cfg.Producer.LingerMS = 1
			reader, writer, err := kafka.NewClients(cfg)
			if err != nil {
				b.Fatal(err)
			}
			defer closeKafkaFixture(b, reader, writer)
			message := kafka.Message{Topic: "bench.v1", Key: []byte("bench"), Value: make([]byte, 1024)}
			ctx := b.Context()
			if err := writer.WriteMessages(ctx, message); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(message.Value)))
			for b.Loop() {
				if err := writer.WriteMessages(ctx, message); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func integrationKafkaConfig(t testing.TB, mode string) config.KafkaConfig {
	t.Helper()
	cfg := config.Default().Kafka
	broker := integrationKafkaBroker(t, map[string]string{"plaintext": "PLAINTEXT", "mtls": "MTLS", "scram": "SCRAM"}[mode])
	cfg.Brokers = []string{broker}
	if mode == "plaintext" {
		return cfg
	}
	dir := os.Getenv("SANDBOX_TEST_KAFKA_SECRET_DIR")
	if dir == "" {
		t.Fatal("SANDBOX_TEST_KAFKA_SECRET_DIR is required for TLS fixture")
	}
	cfg.Security.CAFile = filepath.Join(dir, "ca.pem")
	if mode == "mtls" {
		cfg.Security.Protocol = config.KafkaSSL
		cfg.Security.CertFile = filepath.Join(dir, "sandbox.pem")
		cfg.Security.KeyFile = filepath.Join(dir, "sandbox.key")
	} else {
		cfg.Security.Protocol = config.KafkaSASLSSL
		cfg.Security.Mechanism = config.KafkaSCRAM256
		cfg.Security.UsernameFile = filepath.Join(dir, "username")
		cfg.Security.PasswordFile = filepath.Join(dir, "password")
	}
	return cfg
}

func integrationKafkaBroker(t testing.TB, name string) string {
	t.Helper()
	value := os.Getenv("SANDBOX_TEST_KAFKA_" + name)
	if value == "" {
		t.Skip("run scripts/kafka-security-test.sh to start an isolated Kafka broker")
	}
	return value
}

func closeKafkaFixture(t testing.TB, readers ...io.Closer) {
	t.Helper()
	for _, closer := range readers {
		if err := closer.Close(); err != nil {
			t.Error(err)
		}
	}
}
