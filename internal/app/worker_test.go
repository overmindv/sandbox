package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
)

func TestWorkerLifecycleSmoke(t *testing.T) {
	cfg := config.Default()
	cfg.App.WorkerID = "worker-smoke-test"
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.LogFormat = "json"
	cfg.Observability.LogLevel = "debug"

	var logBuffer bytes.Buffer
	logger := observability.NewLogger(cfg.Observability.LogLevel, cfg.Observability.LogFormat, &logBuffer)
	worker, err := app.NewWorker(cfg, logger)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Shutdown(ctx)
	})

	url := "http://" + worker.HealthAddr() + "/healthz"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build health request: %v", err)
	}
	req.Header.Set(observability.TraceIDHeader, "trace-smoke")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("call health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected health status 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get(observability.TraceIDHeader); got != "trace-smoke" {
		t.Fatalf("expected trace response header, got %q", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status ok, got %#v", payload["status"])
	}
	if payload["ready"] != true {
		t.Fatalf("expected ready=true, got %#v", payload["ready"])
	}
	if payload["trace_id"] != "trace-smoke" {
		t.Fatalf("expected trace_id in response, got %#v", payload["trace_id"])
	}

	readyResp, err := http.Get("http://" + worker.HealthAddr() + "/readyz")
	if err != nil {
		t.Fatalf("call readiness endpoint: %v", err)
	}
	defer readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected readiness status 200, got %d", readyResp.StatusCode)
	}

	if !strings.Contains(logBuffer.String(), "worker started") {
		t.Fatalf("expected startup log, got %s", logBuffer.String())
	}
	if !strings.Contains(logBuffer.String(), "trace-smoke") {
		t.Fatalf("expected trace_id in logs, got %s", logBuffer.String())
	}
}
