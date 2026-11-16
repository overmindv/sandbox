# Kafka Contracts

This document defines the first stable Kafka contract for the sandbox service.

## Topics

Canonical topic names come from `../infra`.

| Topic | Direction | Purpose |
| --- | --- | --- |
| `code-execution.requests.v1` | consume | Code execution requests. Created by infra. |
| `code-execution.retry.v1` | consume/produce | Bounded retry requests with retry headers and backoff. Created by infra. |
| `code-execution.results.v1` | produce | Final execution result. Created by infra. |
| `code-execution.events.v1` | produce | Optional lifecycle events for UI, audit, and observability. Sandbox extension; infra provisioning is still required before production use. |
| `code-execution.deadletter.v1` | produce | Optional messages that cannot be processed safely. Sandbox extension; infra provisioning is still required before production use. |

All topic names include a major contract version. New backward-incompatible contracts use a new topic suffix, for example `code-execution.requests.v2`.

Infra defaults:

- Docker Compose from host: `localhost:29092`.
- Docker Compose from another container on `overmindv_default`: `kafka:9092`.
- Kubernetes namespace: `kafka:9092`.
- Partitions: `3`.
- Replication factor: `1` in the current single-broker environment.
- Retention: `604800000 ms` (seven days).
- Kafka auto topic creation: disabled.

## Message Key

Use the solution UUID as the Kafka message key. In the sandbox domain model this is `submission_id`.

`submission_id` keeps a solution request, retries, events, and final result ordered in one partition. Consumers must treat duplicate messages as possible and deduplicate with their domain idempotency key, usually `attempt_id` for execution attempts and `submission_id` for the business solution.

## Schemas

| Message | Schema |
| --- | --- |
| Request | `api/schemas/sandbox.run.request.v1.json` |
| Event | `api/schemas/sandbox.run.event.v1.json` |
| Result | `api/schemas/sandbox.run.result.v1.json` |
| Dead letter | `api/schemas/sandbox.run.deadletter.v1.json` |

Each payload contains `schema_version`. The value must match the concrete schema, for example `sandbox.run.request.v1`.

## Request

The request contains:

- Identity: `submission_id`, `attempt_id`, `user_id`, `task_id`.
- Language: `language.id`, `language.version`.
- Source code: base64-encoded files with relative paths only.
- Tests: inline files for MVP or a referenced object-storage suite.
- Test modes: `pytest` for Python and `go-test` for Go.
- Limits: CPU, wall time, memory, pids, stdout/stderr, workspace size.
- Trace: `trace_id`.

The sandbox must reject unsafe paths before creating files. Absolute paths, Windows drive paths, and `..` segments are invalid.

For `tests.source=object_storage`, Kafka carries only a test-suite reference:
required `suite_id`, optional `version`, and required lowercase
`checksum_sha256` for the suite manifest object. Hidden test files stay outside
Kafka. The worker reads:

`<tests.object_storage.key_prefix>/test-suites/<suite_id>/<version-or-latest>.json`

The suite manifest uses `schema_version=sandbox.testsuite.v1` and contains
`suite_id`, optional `version`, `mode`, optional `entrypoint`, and base64 test
`files`. The resolved files go through the same file/path/security validation as
inline tests.

## Result

The result is emitted exactly once from the business point of view, but the transport is at-least-once. Downstream services must use `attempt_id` for deduplication.

`logs.stdout` and `logs.stderr` are always safe inline strings. When a stream is
larger than the configured inline threshold, the inline field becomes a preview
and `logs.stdout_artifact` or `logs.stderr_artifact` points to the full stored
artifact. `logs.inline_truncated=true` means the Kafka payload contains a
preview. `logs.truncated=true` means the sandbox or artifact policy actually
discarded bytes.

Artifact references contain:

- deterministic artifact id;
- artifact kind;
- storage provider;
- optional bucket;
- object key;
- content type;
- SHA-256 of stored bytes;
- stored size in bytes;
- truncated flag for the stored object.

Final statuses:

| Status | Meaning |
| --- | --- |
| `passed` | All tests passed. |
| `failed` | Tests ran and at least one test failed. |
| `compile_error` | Code could not be compiled or parsed. |
| `runtime_error` | Program crashed or exited unexpectedly. |
| `tle` | Time limit exceeded. |
| `mle` | Memory limit exceeded. |
| `ole` | Output limit exceeded. |
| `internal_error` | Sandbox infrastructure failed. |
| `invalid_request` | Request failed validation. |
| `unsupported_language` | Language or version is not enabled. |

## Events

Events are optional for business correctness and useful for UX/observability.

Event types:

- `accepted`
- `queued`
- `started`
- `prepared`
- `completed`
- `failed`
- `rejected`
- `retried`

The final state is always represented by a result message. Consumers must not infer final outcome from events alone.

## Retries

Validation and user errors are deterministic and become terminal results without
retry. Infrastructure and retryable internal errors are retried with bounded
backoff.

Retry messages reuse the original request payload and key. The worker adds:

- `x-retry-count`: number of retries already scheduled.
- `x-retry-reason`: normalized failure reason.
- `x-retry-error-class`: `infra` or `internal`.
- `x-retry-not-before`: earliest processing time in RFC3339Nano format.
- `x-original-topic`, `x-original-partition`, `x-original-offset`: source
  request coordinates preserved across retry hops.

`kafka.retry.max_attempts` is the total number of handling attempts, including
the first delivery. After it is exhausted, the worker publishes a DLQ message.

## Dead Letter

Dead-letter messages preserve enough context for debugging:

- Failure reason.
- Retryability.
- Original topic, partition, and offset.
- Original key.
- Original headers.
- Optional original payload as base64.

DLQ is for unsafe or exhausted messages. User-code failures such as failed tests, syntax errors, timeouts, and memory limits are normal results, not DLQ messages.

## Compatibility Rules

Backward-compatible changes:

- Adding an optional field.
- Adding a new `metadata` key.
- Increasing non-security limits.

Backward-incompatible changes:

- Removing or renaming a field.
- Changing field meaning.
- Tightening a required enum in a way that rejects existing messages.
- Changing status semantics.

For breaking changes, create a new schema and topic version. Producers and consumers may run v1 and v2 in parallel during migration.

## Delivery Semantics

Kafka delivery is treated as at-least-once.

The worker must:

- Validate before execution.
- Publish a result before committing the consumed request offset.
- Use `attempt_id` as idempotency key.
- Avoid retrying deterministic validation errors.
- Retry infrastructure errors with a bounded policy.

Downstream consumers must:

- Deduplicate by `attempt_id`.
- Treat result messages as the source of truth.
- Treat events as non-authoritative progress signals.

## Уточнение v1 — OVM-241

Код и JSON Schema поддерживают stdin-режим `execution.mode=execution` с массивом
`inputs`, кроме `pytest`/`go-test`. В stdin-режиме `tests` может отсутствовать или
быть zero-value `{ "mode": "", "source": "" }` после Go-сериализации. Ответ содержит
`status=ok`, `cases`, `summary.cases_total/cases_with_error`. Это описание уже
существующего runtime, а не новая версия topic.

Tasks передаёт `metadata.task_version_id` как UUID неизменяемой версии.
Sandbox сохраняет metadata запроса, но не обязан эхоить её в result: tasks
связывает результат с сохранённой версией по submission/attempt.

Владелец повторов внутри одного attempt_id — sandbox. Новый запуск после
terminal result создаёт tasks с новым attempt_id. Текущий requeue tasks пока
повторно использует ID; исправление/fencing/reaper budget отслеживаются OVM-245.
Повтор Kafka delivery не является новым исполнением.

При исчерпании retry sandbox возвращает сохранённый terminal result либо
internal_error и DLQ. Для attempt_in_progress публикуется DLQ без фиктивного
terminal result активного исполнения.

Важное ограничение предыдущего раздела Compatibility Rules: optional top-level
поле тоже несовместимо со строгим Go decoder, пока он его не знает. Безопасное
расширение v1 — существующий scalar metadata; другие расширения требуют проверки
обоих consumers, breaking changes — нового topic major.

Общие синтетические fixtures находятся в tests/fixtures/execution-contract.
`python scripts/check_execution_contract.py --peer ../tasks` проверяет JSON Schema
и совпадение snapshots обеих сторон (зависимости scripts/requirements-contract.txt).
Go-тест TestSharedExecutionFixtures проверяет реальные decoder и validator.

## Уточнения конверта OVM-225

Kafka key строго равен `submission_id`; пустой или другой ключ отклоняется до
выполнения. `attempt_id` остаётся ключом идемпотентности попытки.
Наличие `execution` требует поддерживаемого режима: неизвестный mode не
переключает запрос на legacy tests. Старые pytest/go-test запросы без execution
сохраняют совместимость.

Opt-in `metadata.result_context_version="1"` требует UUID `task_version_id`.
Результат возвращает контекст версии и checksum упорядоченных входов; каждый
case имеет уникальный индекс. Общие fixtures Python и Go проверяют сериализацию,
схему и pipeline с fake executor; они не доказывают изоляцию реального runtime.
