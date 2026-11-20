# Sandbox Worker Operations

## Endpoints

- Health: `GET /healthz`
- Readiness: `GET /readyz`
- Metrics: `GET /metrics`

## Worker Shutdown

On SIGTERM, the worker reports not ready and stops taking requests from Kafka.
It finishes the message it already admitted, including result publication and
offset commit, within `SANDBOX_SHUTDOWN_TIMEOUT_MS`. The Kubernetes worker has
30 seconds inside a 35-second termination grace period. Keep the outer grace
period longer than the worker timeout when overriding either value.

At the deadline, the worker cancels processing and exits with an error. An
uncommitted message can be delivered again. With PostgreSQL, the next owner
replays a stored result or reports `execution_interrupted` for an unfinished
attempt; it does not execute the same attempt again.

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

## Performance

Performance checks are split into two layers:

- Go microbenchmarks for worker-side Python adapter and pytest result parsing.
- Live load tests through Kafka, sandbox-worker, go-judge, and result Kafka.

Run Python microbenchmarks:

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.gocache'
go test ./internal/languages/python -run ^$ -bench Benchmark -benchmem -count=5
```

On systems with `make`, the same check is available as `make perf-bench`.

Run optional live go-judge benchmarks when host networking can reach go-judge:

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.gocache'
$env:SANDBOX_GOJUDGE_BENCH = '1'
$env:SANDBOX_GOJUDGE_BASE_URL = 'http://127.0.0.1:5050'
go test ./internal/execution/gojudge -run ^$ -bench BenchmarkLiveGoJudgePythonExecution -benchmem -benchtime=5x
```

Run end-to-end Kafka load tests:

```powershell
.\scripts\load-test.ps1 -Jobs 20 -TimeoutSeconds 180 -RunLabel local-python-20 -ReportPath .tmp\load-test-live-20-report.json
.\scripts\load-test.ps1 -Language go -Jobs 20 -TimeoutSeconds 180 -RunLabel local-go-20 -ReportPath .tmp\load-test-go-live-20-report.json
```

On systems with `make`, the same default load shape is available as
`make perf-load` for Python and `make perf-load-go` for Go.

The load-test report uses `schema_version=sandbox.load_test.report.v1`.
`throughput_jobs_per_sec` is measured from the first generated request timestamp
to the last result timestamp. `wall_throughput_jobs_per_sec` includes script and
`kafka-console-consumer` overhead. `queue_ms` currently starts at
`RunRequest.created_at`, so it includes Kafka wait time plus scheduler queue time.

### Baseline: 2026-08-23

Environment:

- Windows host, AMD Ryzen 9 8945HX, Go test target `windows/amd64`.
- Local Docker Compose stack: Kafka `apache/kafka:3.9.1`, go-judge
  `overmindv/sandbox-gojudge:local`, worker
  `overmindv/sandbox-worker:local`.
- Worker executor: `gojudge`, `SANDBOX_GOJUDGE_BASE_URL=http://gojudge:5050`.
- Scheduler config: `max_in_flight=4`, `max_in_flight_per_language.python=4`,
  queue depth `16`.
- Local topics: request/retry/result/event partitions `6`, DLQ partitions `3`.

Worker-side Python microbenchmarks, median of 5 runs:

| Benchmark | Median ns/op | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkBuildCommandPythonPytest` | 3,661 | 3,616 | 11 |
| `BenchmarkBuildCommandPythonPytestManyFiles` | 11,678 | 9,056 | 31 |
| `BenchmarkBuildCommandPythonPytestParallel` | 1,896 | 3,616 | 11 |
| `BenchmarkParsePytestReport` | 169,710 | 26,872 | 220 |

End-to-end live load test results:

| Run | Jobs | Passed | Pipeline throughput | Pipeline window | p95 total | p95 queue | p95 run |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| after go-judge restart | 1 | 1 | 0.33 jobs/s | 3.025 s | 3,013 ms | 2,765 ms | 248 ms |
| warm single | 1 | 1 | 0.35 jobs/s | 2.865 s | 2,852 ms | 2,606 ms | 246 ms |
| warm batch | 4 | 4 | 1.05 jobs/s | 3.821 s | 3,723 ms | 3,476 ms | 255 ms |
| warm batch | 20 | 20 | 2.41 jobs/s | 8.315 s | 7,962 ms | 7,728 ms | 266 ms |

Interpretation:

- Python adapter overhead is negligible compared to sandbox execution.
- Warm Python/go-judge execution for the tiny pytest fixture is about
  `250-270 ms` at p95 inside the executor.
- Current effective worker parallelism is close to one Kafka job per worker
  process because `Worker.runProcessorLoop` calls `ProcessOnce` synchronously and
  waits for result publication before fetching the next message. The scheduler
  has four slots, but the Kafka loop does not feed it concurrently yet.
- The high `queue_ms` in batch runs is therefore expected and represents Kafka
  backlog plus scheduler queue time, not Python runtime.

### Initial Defaults

Default Python limits remain intentionally conservative:

- `cpu_ms=2000`
- `wall_ms=5000`
- `memory_mb=128`
- `pids=32`
- `stdout_bytes=32768`
- `stderr_bytes=32768`
- `workspace_bytes=1048576`

These defaults leave roughly 18x wall-time headroom over the measured tiny
pytest p95 run time while still bounding hostile workloads.

Kafka partitions:

- Local: keep `6` request/retry/result/event partitions and `3` DLQ partitions.
- Dev/stage starting point: `12` request/result/retry partitions for up to
  `6-12` worker replicas.
- Production starting point: choose partitions at least `2x` the planned worker
  replica count, and at least the expected total concurrent processing lanes
  once the processor loop supports concurrent fetch/handle/commit.

Worker concurrency:

- Current production-safe scaling knob is worker replicas, not
  `max_in_flight`, because Kafka processing is synchronous per worker process.
- With the current processor loop, use one in-flight Kafka job per worker
  process and scale horizontally up to the request topic partition count.
- Keep `scheduler.max_in_flight=4` and `python=4` as the next-stage defaults so
  the scheduler is ready for concurrent Kafka processing, but do not expect a
  single worker process to use all slots until the processor loop is parallelized
  with ordered/at-least-once-safe commits.

## Приватные diagnostics (OVM-228)

`diagnostics_visibility` private по умолчанию; public только explicit samples без
закрытого suite. Private logs/error details/case stderr не создают object artifacts.
Case stdout сохраняется исключительно в доверенном result envelope для checker Tasks.
Доступ к result topic, outbox и run storage разрешается только worker/tasks ролям;
публичные consumers и общий operator artifact bucket не должны их читать.
Отдельные подробные private object artifacts не сохраняются (нет дополнительной копии
или отдельного TTL). Общая retention/GC данных исполнения — задача OVM-232.
Обычный DLQ содержит координаты текущего broker record, validated UUID и reason,
без payload/headers/raw errors. Replay payload из DLQ больше не поддерживается;
новую попытку должен выдавать Tasks с новым attempt ID.
Неизвестный формат trace ID в operator logs заменяется стабильным хешем.
Порядок обновления контракта: Tasks consumer, затем sandbox producer.

## Ограниченное восстановление (OVM-230)

Tasks задаёт `metadata.attempt_deadline` (RFC3339Nano), равный сроку текущего
attempt. Sandbox не запускает просроченную попытку, ограничивает контекст обработчика
остатком срока и не планирует retry за этой границей. Итог — infrastructure error
`attempt_deadline_exceeded` и metadata-only DLQ. Максимум 5 обработок и 30 секунд
backoff независимо от завышенной конфигурации. Старые producers без deadline
сохраняют ограничение числа обработок; при обновлении сначала обновляется sandbox.

Replay выполняется через внутренний Tasks endpoint
`POST /v1/admin/code-submissions/{id}/replay` с trusted admin actor и
`{"expected_attempt_id":"UUID"}`. Допустимы завершённые infrastructure/checker errors
или queued с истёкшим сроком. Tasks сохраняет исходную версию и код, выдаёт новый ID,
проверяет ёмкость очереди и атомарно пишет audit (actor, старый/новый ID, число попыток)
и outbox. Повтор со старым expected ID возвращает 409. Успешные и ошибочные решения
студента replay не допускают. Raw DLQ payload для восстановления не нужен.
