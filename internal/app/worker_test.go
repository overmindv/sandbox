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
	cfg.Observability.MetricsEnabled = false
	cfg.Observability.LogFormat = "json"
	cfg.Observability.LogLevel = "debug"

	var logBuffer bytes.Buffer
	logger := observability.NewLogger(cfg.Observability.LogLevel, cfg.Observability.LogFormat, &logBuffer)
	worker, err := app.NewWorker(cfg, logger)
	if err != nil {
		t.Errorf("new worker: %v", err)
	}

	if err := worker.Start(context.Background()); err != nil {
		t.Errorf("start worker: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Shutdown(ctx)
	})

	url := "http://" + worker.HealthAddr() + "/healthz"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Errorf("build health request: %v", err)
	}
	req.Header.Set(observability.TraceIDHeader, "11111111-1111-4111-8111-111111111111")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Errorf("call health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected health status 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get(observability.TraceIDHeader); got != "11111111-1111-4111-8111-111111111111" {
		t.Errorf("expected trace response header, got %q", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Errorf("decode health response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Errorf("expected status ok, got %#v", payload["status"])
	}
	if payload["ready"] != true {
		t.Errorf("expected ready=true, got %#v", payload["ready"])
	}
	if payload["trace_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Errorf("expected trace_id in response, got %#v", payload["trace_id"])
	}

	readyResp, err := http.Get("http://" + worker.HealthAddr() + "/readyz")
	if err != nil {
		t.Errorf("call readiness endpoint: %v", err)
	}
	defer readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Errorf("expected readiness status 200, got %d", readyResp.StatusCode)
	}

	if !strings.Contains(logBuffer.String(), "worker started") {
		t.Errorf("expected startup log, got %s", logBuffer.String())
	}
	if !strings.Contains(logBuffer.String(), "11111111-1111-4111-8111-111111111111") {
		t.Errorf("expected trace_id in logs, got %s", logBuffer.String())
	}
}
