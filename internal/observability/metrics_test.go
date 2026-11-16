package observability

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSchedulerMetricsTracksKafkaResultsAndDurations(t *testing.T) {
	metrics := NewSchedulerMetrics()

	metrics.IncConsumed("code-execution.requests.v1")
	metrics.IncProduced("code-execution.results.v1")
	metrics.IncMessageFailure("produce_failed")
	metrics.IncResultStatus("passed")
	metrics.IncResultLanguage("python")
	metrics.ObserveProcessingDuration("execute", 15*time.Millisecond)
	metrics.ObserveProcessingDuration("execute", 25*time.Millisecond)
	metrics.ObserveQueueLag("code-execution.requests.v1", 200*time.Millisecond)
	metrics.ObserveExecutorLatency("python", 10*time.Millisecond)

	snapshot := metrics.Snapshot()
	if snapshot.Consumed["code-execution.requests.v1"] != 1 {
		t.Errorf("expected consumed metric, got %#v", snapshot.Consumed)
	}
	if snapshot.Produced["code-execution.results.v1"] != 1 {
		t.Errorf("expected produced metric, got %#v", snapshot.Produced)
	}
	if snapshot.MessageFailures["produce_failed"] != 1 {
		t.Errorf("expected failure metric, got %#v", snapshot.MessageFailures)
	}
	if snapshot.ResultStatuses["passed"] != 1 {
		t.Errorf("expected result status metric, got %#v", snapshot.ResultStatuses)
	}
	if snapshot.ResultLanguages["python"] != 1 {
		t.Errorf("expected result language metric, got %#v", snapshot.ResultLanguages)
	}

	executeStats := snapshot.ProcessingDurationsByStage["execute"]
	if executeStats.Count != 2 || executeStats.SumMS != 40 || executeStats.MaxMS != 25 {
		t.Errorf("expected execute duration stats count=2 sum=40 max=25, got %#v", executeStats)
	}
	lagStats := snapshot.QueueLagByTopic["code-execution.requests.v1"]
	if lagStats.Count != 1 || lagStats.SumMS != 200 || lagStats.MaxMS != 200 {
		t.Errorf("expected queue lag stats, got %#v", lagStats)
	}
	executorStats := snapshot.ExecutorLatencyByLanguage["python"]
	if executorStats.Count != 1 || executorStats.SumMS != 10 || executorStats.MaxMS != 10 {
		t.Errorf("expected executor latency stats, got %#v", executorStats)
	}
}

func TestTracerAndAuditEmitStructuredLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := NewLogger("debug", "json", &logs)
	ctx := WithTraceID(context.Background(), "trace-observability-test")

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	tracer := NewTracer(true, logger)
	tracer.now = func() time.Time { return now }

	spanCtx, endSpan := tracer.Start(ctx, "consume", "topic", "requests")
	now = now.Add(17 * time.Millisecond)
	endSpan(nil, "offset", 10)

	audit := NewAuditLogger(true, logger)
	audit.SecurityReject(spanCtx, "payload_rejected", errors.New("payload too large"), "offset", 10)

	output := logs.String()
	for _, expected := range []string{
		safeTraceID("trace-observability-test"),
		"trace span ended",
		"duration_ms",
		"security_reject",
		"payload_rejected",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected log output to contain %q, got %s", expected, output)
		}
	}
}
