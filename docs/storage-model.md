# Storage Model

The sandbox database is an audit and idempotency store. Kafka stays the queue, and
Postgres records what happened so retries, rebalances, replay, and later analytics
do not change the business result.

## Core Shape

- `runs` stores the business execution unit. A run is keyed by an internal
  deterministic `run_id` and also carries external references such as
  `submission_id`, `user_id`, `task_id`, and optional `task_version_id`.
- `attempts` stores physical executions. Multiple attempts can belong to the
  same run; `attempt_id` remains the Kafka idempotency anchor.
- `idempotency_keys` binds external idempotency keys to the exact run, attempt,
  and request hash.
- `worker_instances` and `worker_capabilities` describe which worker handled an
  attempt and what per-language capacity it advertised.
- `run_files` and `run_tests` are immutable request snapshots. They normalize the
  fields used for lookup and analytics while `request_json` keeps the complete
  contract payload.
- `attempt_test_results` stores individual test outcomes extracted from
  `RunResult`.
- `artifacts` stores metadata for stdout, stderr, pytest reports, compile logs,
  and archives. Large stdout/stderr content is written to object storage and
  `result_json` keeps inline previews plus artifact references.
- `outbox_events` is the durable event handoff for result, retry, and DLQ
  publication.

## IDs And Hashes

The service generates UUIDs in application code and does not require Postgres
extensions. `run_id`, file IDs, test IDs, artifact IDs, and result outbox event
IDs are deterministic from stable request fields. This makes duplicate delivery
safe even when a worker is restarted between storing and publishing.

`request_hash` excludes `attempt_id`, `trace_id`, and `created_at`, because those
belong to physical delivery and tracing. Code, tests, limits, language, and
business references remain part of the hash.

## Postgres vs Object Storage

Postgres stores:

- normalized identifiers, statuses, timestamps, hashes, and indexes;
- compact immutable `request_json` and `result_json` snapshots;
- test result rows and artifact metadata;
- outbox payloads needed for reliable Kafka publication.

Object storage stores:

- large stdout/stderr once `storage.artifacts.enabled=true`;
- pytest JSON reports;
- compile logs above inline limits;
- copied-out archives and debugging bundles.

Artifact object keys are deterministic by run, attempt, and artifact kind:
`<key_prefix>/runs/<run_id>/attempts/<attempt_id>/<kind>.txt`. This makes retry
after an artifact upload safe: the same attempt rewrites the same object with
the same content hash.

## Retention

Retention is configured under `storage.retention`. The initial policy is only
configuration; cleanup jobs are intentionally separate from the hot path.

- completed runs: keep long enough for audit and user support;
- artifacts: shorter than runs because bulky data moves to object storage;
- published outbox events: short retention after successful publication;
- failed outbox events and idempotency keys: longer retention for investigation;
- stale worker heartbeat: controls when operators can consider a worker dead.
