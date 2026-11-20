package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/scheduler"
)

type MetricsServer struct {
	addr       string
	dispatcher *scheduler.Dispatcher
	logger     *observability.Logger
	enabled    bool
	server     *http.Server
	listener   net.Listener
	errCh      chan error
	mu         sync.RWMutex
}

func NewMetricsServer(addr string, dispatcher *scheduler.Dispatcher, logger *observability.Logger, enabled bool) *MetricsServer {
	return &MetricsServer{
		addr:       addr,
		dispatcher: dispatcher,
		logger:     logger,
		enabled:    enabled,
		errCh:      make(chan error, 1),
	}
}

func (s *MetricsServer) Start(ctx context.Context) error {
	if s == nil || !s.enabled {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	s.mu.Lock()
	s.listener = listener
	s.server = server
	s.mu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errCh <- err
			return
		}
		s.errCh <- nil
	}()

	s.logger.Info(ctx, "metrics server started", "addr", listener.Addr().String())
	return nil
}

func (s *MetricsServer) Shutdown(ctx context.Context) error {
	if s == nil || !s.enabled {
		return nil
	}

	s.mu.RLock()
	server := s.server
	s.mu.RUnlock()
	if server == nil {
		return nil
	}

	err := server.Shutdown(ctx)
	select {
	case serveErr := <-s.errCh:
		if err == nil {
			err = serveErr
		}
	default:
	}
	return err
}

func (s *MetricsServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(s.render()))
}

func (s *MetricsServer) render() string {
	var snapshot observability.SchedulerMetricsSnapshot
	var slots scheduler.SlotStats
	if s.dispatcher != nil {
		snapshot = s.dispatcher.Metrics()
		slots = s.dispatcher.Stats()
	}

	var b strings.Builder
	writeHelp(&b, "sandbox_scheduler_queue_depth", "gauge", "Current scheduler queue depth.")
	writeGauge(&b, "sandbox_scheduler_queue_depth", snapshot.QueueDepth)
	writeHelp(&b, "sandbox_scheduler_queue_capacity", "gauge", "Configured scheduler queue capacity.")
	writeGauge(&b, "sandbox_scheduler_queue_capacity", snapshot.QueueCapacity)
	writeHelp(&b, "sandbox_scheduler_in_flight", "gauge", "Current number of in-flight jobs.")
	writeGauge(&b, "sandbox_scheduler_in_flight", slots.InFlight)
	writeHelp(&b, "sandbox_scheduler_in_flight_by_language", "gauge", "Current in-flight jobs by language.")
	writeGaugeMap(&b, "sandbox_scheduler_in_flight_by_language", "language", slots.ByLanguage)
	writeHelp(&b, "sandbox_scheduler_slots_capacity", "gauge", "Configured execution slots per worker.")
	writeGauge(&b, "sandbox_scheduler_slots_capacity", slots.MaxGlobal)
	writeHelp(&b, "sandbox_scheduler_slots_utilization_percent", "gauge", "Highest current utilization of the global or per-language slot pools.")
	writeGauge(&b, "sandbox_scheduler_slots_utilization_percent", slotUtilization(slots))
	writeHelp(&b, "sandbox_scheduler_jobs_submitted_total", "counter", "Total submitted jobs by language.")
	writeCounterMap(&b, "sandbox_scheduler_jobs_submitted_total", "language", snapshot.Submitted)
	writeHelp(&b, "sandbox_scheduler_jobs_rejected_total", "counter", "Total rejected jobs by reason.")
	writeCounterMap(&b, "sandbox_scheduler_jobs_rejected_total", "reason", snapshot.Rejected)
	writeHelp(&b, "sandbox_scheduler_jobs_started_total", "counter", "Total started jobs by language.")
	writeCounterMap(&b, "sandbox_scheduler_jobs_started_total", "language", snapshot.Started)
	writeHelp(&b, "sandbox_scheduler_jobs_completed_total", "counter", "Total completed jobs by status.")
	writeCounterMap(&b, "sandbox_scheduler_jobs_completed_total", "status", snapshot.Completed)
	writeHelp(&b, "sandbox_scheduler_jobs_timed_out_total", "counter", "Total timed out jobs by language.")
	writeCounterMap(&b, "sandbox_scheduler_jobs_timed_out_total", "language", snapshot.TimedOut)
	writeHelp(&b, "sandbox_kafka_messages_consumed_total", "counter", "Total consumed Kafka messages by topic.")
	writeCounterMap(&b, "sandbox_kafka_messages_consumed_total", "topic", snapshot.Consumed)
	writeHelp(&b, "sandbox_kafka_messages_produced_total", "counter", "Total produced Kafka messages by topic.")
	writeCounterMap(&b, "sandbox_kafka_messages_produced_total", "topic", snapshot.Produced)
	writeHelp(&b, "sandbox_kafka_message_failures_total", "counter", "Total Kafka message processing or publishing failures by reason.")
	writeCounterMap(&b, "sandbox_kafka_message_failures_total", "reason", snapshot.MessageFailures)
	writeHelp(&b, "sandbox_results_total", "counter", "Total terminal results by execution status.")
	writeCounterMap(&b, "sandbox_results_total", "status", snapshot.ResultStatuses)
	writeHelp(&b, "sandbox_results_by_language_total", "counter", "Total terminal results by language.")
	writeCounterMap(&b, "sandbox_results_by_language_total", "language", snapshot.ResultLanguages)
	writeHelp(&b, "sandbox_worker_processing_duration_ms", "summary", "Worker processing duration in milliseconds by stage.")
	writeDurationStatsMap(&b, "sandbox_worker_processing_duration_ms", "stage", snapshot.ProcessingDurationsByStage)
	writeHelp(&b, "sandbox_kafka_queue_lag_ms", "summary", "Kafka queue lag in milliseconds by consumed topic.")
	writeDurationStatsMap(&b, "sandbox_kafka_queue_lag_ms", "topic", snapshot.QueueLagByTopic)
	writeHelp(&b, "sandbox_executor_latency_ms", "summary", "Executor latency in milliseconds by language.")
	writeDurationStatsMap(&b, "sandbox_executor_latency_ms", "language", snapshot.ExecutorLatencyByLanguage)
	writeHelp(&b, "sandbox_kafka_retries_scheduled_total", "counter", "Total retry messages scheduled by reason.")
	writeCounterMap(&b, "sandbox_kafka_retries_scheduled_total", "reason", snapshot.RetriesScheduled)
	writeHelp(&b, "sandbox_kafka_retries_delayed_total", "counter", "Total retry messages delayed because not-before was in the future.")
	writeCounterMap(&b, "sandbox_kafka_retries_delayed_total", "reason", snapshot.RetriesDelayed)
	writeHelp(&b, "sandbox_kafka_retries_exhausted_total", "counter", "Total messages sent to DLQ after retry attempts were exhausted.")
	writeCounterMap(&b, "sandbox_kafka_retries_exhausted_total", "reason", snapshot.RetriesExhausted)
	writeHelp(&b, "sandbox_kafka_dlq_published_total", "counter", "Total dead letter messages published by reason.")
	writeCounterMap(&b, "sandbox_kafka_dlq_published_total", "reason", snapshot.DLQPublished)
	writeHelp(&b, "sandbox_kafka_error_results_published_total", "counter", "Total terminal error results published by reason.")
	writeCounterMap(&b, "sandbox_kafka_error_results_published_total", "reason", snapshot.ErrorResults)
	writeHelp(&b, "sandbox_artifacts_stored_total", "counter", "Total artifacts stored by kind.")
	writeCounterMap(&b, "sandbox_artifacts_stored_total", "kind", snapshot.ArtifactsStored)
	writeHelp(&b, "sandbox_artifact_bytes_stored_total", "counter", "Total artifact bytes stored by kind.")
	writeCounterMap(&b, "sandbox_artifact_bytes_stored_total", "kind", snapshot.ArtifactBytes)
	writeHelp(&b, "sandbox_artifact_store_failures_total", "counter", "Total artifact storage failures by reason.")
	writeCounterMap(&b, "sandbox_artifact_store_failures_total", "reason", snapshot.ArtifactFailures)
	return b.String()
}

func slotUtilization(slots scheduler.SlotStats) int {
	if slots.MaxGlobal <= 0 {
		return 0
	}
	percent := slots.InFlight * 100 / slots.MaxGlobal
	for language, capacity := range slots.MaxPerLanguage {
		if capacity > 0 {
			percent = max(percent, slots.ByLanguage[language]*100/capacity)
		}
	}
	return percent
}

func writeHelp(b *strings.Builder, name, metricType, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, metricType)
}

func writeGauge(b *strings.Builder, name string, value int) {
	fmt.Fprintf(b, "%s %d\n", name, value)
}

func writeGaugeMap(b *strings.Builder, name string, label string, values map[string]int) {
	keys := sortedIntKeys(values)
	for _, key := range keys {
		fmt.Fprintf(b, "%s{%s=%q} %d\n", name, label, escapeLabelValue(key), values[key])
	}
}

func writeCounterMap(b *strings.Builder, name string, label string, values map[string]int64) {
	keys := sortedInt64Keys(values)
	for _, key := range keys {
		fmt.Fprintf(b, "%s{%s=%q} %d\n", name, label, escapeLabelValue(key), values[key])
	}
}

func writeDurationStatsMap(b *strings.Builder, name string, label string, values map[string]observability.DurationStats) {
	keys := sortedDurationKeys(values)
	for _, key := range keys {
		escaped := escapeLabelValue(key)
		stats := values[key]
		fmt.Fprintf(b, "%s_count{%s=%q} %d\n", name, label, escaped, stats.Count)
		fmt.Fprintf(b, "%s_sum{%s=%q} %d\n", name, label, escaped, stats.SumMS)
		fmt.Fprintf(b, "%s_max{%s=%q} %d\n", name, label, escaped, stats.MaxMS)
	}
}

func sortedIntKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedInt64Keys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedDurationKeys(values map[string]observability.DurationStats) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
