package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/scheduler"
)

func TestMetricsServerRendersObservabilityMetrics(t *testing.T) {
	metrics := observability.NewSchedulerMetrics()
	metrics.IncConsumed("code-execution.requests.v1")
	metrics.IncProduced("code-execution.results.v1")
	metrics.IncMessageFailure("produce_failed")
	metrics.IncResultStatus("passed")
	metrics.IncResultLanguage("python")
	metrics.ObserveProcessingDuration("execute", 25*time.Millisecond)
	metrics.ObserveQueueLag("code-execution.requests.v1", 500*time.Millisecond)
	metrics.ObserveExecutorLatency("python", 12*time.Millisecond)

	cfg := config.Default().Scheduler
	dispatcher, err := scheduler.NewDispatcher(cfg, scheduler.JobHandlerFunc(func(context.Context, scheduler.Job) (any, error) {
		return nil, nil
	}), metrics, observability.NewLogger("error", "json", nil), scheduler.ExecutorHealthFunc(func() bool { return true }))
	if err != nil {
		t.Errorf("new dispatcher: %v", err)
		return
	}

	server := NewMetricsServer("127.0.0.1:0", dispatcher, observability.NewLogger("error", "json", nil), true)
	rendered := server.render()

	for _, expected := range []string{
		`sandbox_kafka_messages_consumed_total{topic="code-execution.requests.v1"} 1`,
		`sandbox_kafka_messages_produced_total{topic="code-execution.results.v1"} 1`,
		`sandbox_kafka_message_failures_total{reason="produce_failed"} 1`,
		`sandbox_results_total{status="passed"} 1`,
		`sandbox_results_by_language_total{language="python"} 1`,
		`sandbox_worker_processing_duration_ms_count{stage="execute"} 1`,
		`sandbox_kafka_queue_lag_ms_max{topic="code-execution.requests.v1"} 500`,
		`sandbox_executor_latency_ms_sum{language="python"} 12`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("expected rendered metrics to contain %q, got:\n%s", expected, rendered)
		}
	}
}
