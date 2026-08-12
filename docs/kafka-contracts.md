# Kafka Contracts

This document defines the first stable Kafka contract for the sandbox service.

## Topics

Canonical topic names come from `../infra`.

| Topic | Direction | Purpose |
| --- | --- | --- |
| `code-execution.requests.v1` | consume | Code execution requests. Created by infra. |
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
- Limits: CPU, wall time, memory, pids, stdout/stderr, workspace size.
- Trace: `trace_id`.

The sandbox must reject unsafe paths before creating files. Absolute paths, Windows drive paths, and `..` segments are invalid.

## Result

The result is emitted exactly once from the business point of view, but the transport is at-least-once. Downstream services must use `attempt_id` for deduplication.

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

## Dead Letter

Dead-letter messages preserve enough context for debugging:

- Failure reason.
- Retryability.
- Original topic, partition, and offset.
- Original key.
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
