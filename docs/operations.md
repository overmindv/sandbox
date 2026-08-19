# Sandbox Worker Operations

## Endpoints

- Health: `GET /healthz`
- Readiness: `GET /readyz`
- Metrics: `GET /metrics`

## Logs

The worker writes structured logs through `slog`. Use JSON in shared environments and console output locally:

- `SANDBOX_LOG_FORMAT=json`
- `SANDBOX_LOG_LEVEL=info`
- `SANDBOX_TRACING_ENABLED=true`
- `SANDBOX_AUDIT_ENABLED=true`

Every Kafka message carries `trace_id` from `X-Trace-Id` when present. Missing trace IDs are generated inside the worker.

## Metrics

Core Prometheus metrics:

- `sandbox_kafka_messages_consumed_total`
- `sandbox_kafka_messages_produced_total`
- `sandbox_kafka_message_failures_total`
- `sandbox_worker_processing_duration_ms_count`
- `sandbox_worker_processing_duration_ms_sum`
- `sandbox_worker_processing_duration_ms_max`
- `sandbox_kafka_queue_lag_ms_max`
- `sandbox_executor_latency_ms_count`
- `sandbox_executor_latency_ms_sum`
- `sandbox_results_total`
- `sandbox_results_by_language_total`
- `sandbox_scheduler_queue_depth`
- `sandbox_scheduler_in_flight`
- `sandbox_kafka_dlq_published_total`

## Tracing

When tracing is enabled, the worker emits span logs for:

- `consume`
- `validate`
- `prepare`
- `execute`
- `publish`

These are structured log spans, not a remote tracing exporter yet. They are intentionally shaped so an OpenTelemetry exporter can replace the current logger-backed tracer later.

## Audit

Security-relevant rejects are logged with `audit_event=security_reject`. Current audit events cover oversized or malformed payloads, forbidden paths, archive guard failures, and request rejects after object-storage test resolution.

## Local Alerts

Prometheus loads `deployments/docker/prometheus-alerts.yaml` in the observability profile. Current draft alerts:

- `SandboxHighDLQ`
- `SandboxHighInternalErrors`
- `SandboxHighKafkaLag`
- `SandboxExecutorUnavailable`

## Dashboard

The Grafana dashboard draft is stored at `deployments/observability/grafana-dashboard.json`.
