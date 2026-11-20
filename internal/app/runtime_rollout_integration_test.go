//go:build integration

package app_test

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution/gojudge"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/storage"
)

func TestKafkaWorkerDrainKeepsRuntimeRelease(t *testing.T) {
	oldURL := rolloutEndpoint(t, "SANDBOX_TEST_ROLLOUT_OLD_URL")
	newURL := rolloutEndpoint(t, "SANDBOX_TEST_ROLLOUT_NEW_URL")
	broker := integrationKafkaBroker(t, "PLAINTEXT")
	host, _, err := net.SplitHostPort(broker)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("isolated loopback Kafka broker required")
	}
	dsn := os.Getenv("SANDBOX_TEST_POSTGRES_DSN")
	db, err := url.Parse(dsn)
	if err != nil || db.Hostname() != "127.0.0.1" || db.Path != "/sandbox_test" {
		t.Fatal("isolated sandbox_test PostgreSQL database required")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	id := "rollout-" + storage.StableUUID(time.Now().String())
	cfg := drainConfig()
	cfg.App.WorkerID = id + "-old"
	cfg.App.ShutdownTimeoutMS = 6000
	cfg.Kafka.Brokers = []string{broker}
	cfg.Kafka.GroupID = id
	cfg.Kafka.Consumer.AutoOffsetReset = "earliest"
	cfg.Kafka.Topics = config.KafkaTopics{
		Requests: id + ".requests", Retry: id + ".retry", Events: id + ".events",
		Results: id + ".results", DeadLetter: id + ".deadletter",
	}
	cfg.Scheduler.MaxInFlight = 16
	cfg.Scheduler.MaxInFlightPerLanguage = map[string]int{"python": 16}
	cfg.Observability.MetricsEnabled = true
	cfg.Server.MetricsAddr = "127.0.0.1:0"
	cfg.Storage.Type, cfg.Storage.Postgres.DSN = "postgres", dsn
	createDrainTopics(t, ctx, broker, cfg.Kafka.Topics)

	input := &kafkago.Writer{Addr: kafkago.TCP(broker), RequiredAcks: kafkago.RequireAll}
	output := kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{broker}, Topic: cfg.Kafka.Topics.Results})
	defer closeKafkaFixture(t, input, output)

	attempts := make([]string, 0, 3)
	for _, suffix := range []string{"-1", "-2", "-3"} {
		req := validPipelineExecutionRunRequest()
		req.SubmissionID = storage.StableUUID(id, suffix)
		req.AttemptID = storage.StableUUID(req.SubmissionID, "attempt")
		req.Execution.Inputs = []string{""}
		req.Code.Files[0].ContentB64 = base64.StdEncoding.EncodeToString([]byte(
			"import sys,time\ntime.sleep(1.5)\nprint(sys.version.split()[0])\n"))
		value, err := kafka.EncodeRunRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := input.WriteMessages(ctx, kafkago.Message{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: value}); err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, req.AttemptID)
	}

	forwarded := make(chan struct{})
	proxy := httptest.NewServer(trackRuntimeRequest(oldURL, forwarded))
	defer proxy.Close()
	old, err := gojudge.NewExecutor(config.GoJudgeClient{BaseURL: proxy.URL, RequestTimeoutMS: 6000})
	if err != nil {
		t.Fatal(err)
	}
	worker, closed := newLiveDrainWorker(t, cfg, old)
	stop, done := startDrainWorker(t, worker)
	select {
	case <-forwarded:
	case <-ctx.Done():
		t.Fatal("first job did not reach its runtime", ctx.Err())
	}
	assertSaturatedWorker(t, ctx, worker.MetricsAddr())
	stop()
	waitNotReady(t, worker)
	if err := waitDrainResult(t, done); err != nil {
		t.Fatal("old worker did not drain", err)
	}
	waitDrainSignal(t, closed)
	first := readDrainResult(t, ctx, output, attempts[0])
	assertVersionResult(t, first, cfg.App.WorkerID, "3.13.5")

	cfg.App.WorkerID = id + "-new"
	nextRuntime, err := gojudge.NewExecutor(config.GoJudgeClient{BaseURL: newURL.String(), RequestTimeoutMS: 6000})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := newLiveDrainWorker(t, cfg, nextRuntime)
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
	for _, attempt := range attempts[1:] {
		if err := next.ProcessOnce(ctx); err != nil {
			t.Fatal(err)
		}
		result := readDrainResult(t, ctx, output, attempt)
		assertVersionResult(t, result, cfg.App.WorkerID, "3.12.16")
	}
}

func assertSaturatedWorker(t *testing.T, ctx context.Context, addr string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal("read saturation metrics", resp.StatusCode, err)
	}
	for _, line := range []string{
		"sandbox_scheduler_slots_capacity 16\n",
		"sandbox_scheduler_execution_capacity 1\n",
		"sandbox_scheduler_slots_utilization_percent 100\n",
	} {
		if !strings.Contains(string(body), line) {
			t.Error("missing saturation sample", strings.TrimSpace(line))
		}
	}
}

func rolloutEndpoint(t *testing.T, env string) *url.URL {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skip("set isolated old and new go-judge fixture URLs for the rollout test")
	}
	value, err := url.Parse(raw)
	if err != nil || value.Scheme != "http" || value.Hostname() != "127.0.0.1" || value.Port() == "" || value.User != nil {
		t.Fatal("isolated loopback go-judge required")
	}
	return value
}

func trackRuntimeRequest(endpoint *url.URL, forwarded chan struct{}) http.Handler {
	var once sync.Once
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/run" {
			trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					once.Do(func() { close(forwarded) })
				}
			}}
			r = r.WithContext(httptrace.WithClientTrace(r.Context(), trace))
		}
		proxy.ServeHTTP(w, r)
	})
}

func assertVersionResult(t *testing.T, result contracts.RunResult, worker, version string) {
	t.Helper()
	if result.Status != contracts.StatusOK || result.WorkerID != worker || len(result.Cases) != 1 {
		t.Fatal("runtime job did not finish on its worker", result.Status, result.WorkerID, result.Error)
	}
	if got := strings.TrimSpace(result.Cases[0].Stdout); got != version {
		t.Error("execution changed runtime revision", got, version)
	}
}
