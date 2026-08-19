# Configuration

The sandbox worker is configured through defaults, YAML files, and environment variables.

## Load Order

Configuration precedence is:

1. Environment variables.
2. The YAML file selected by `SANDBOX_CONFIG_FILE`.
3. Built-in defaults.

This means production secrets and deployment-specific endpoints must come from environment variables, while stable service shape lives in YAML.

## Files

| File | Purpose |
| --- | --- |
| `configs/local.yaml` | Local development profile. Uses the fake executor by default. |
| `configs/dev.yaml` | Shared dev/stage profile. Uses go-judge and Postgres. |
| `configs/prod.yaml` | Production profile. Uses go-judge, Postgres, object storage, strict limits. |
| `configs/languages.yaml` | Language registry and adapter metadata. |
| `configs/limits.yaml` | Default and hard execution limits. |
| `configs/gojudge-mount.yaml` | go-judge workspace and copy-in/copy-out policy. |
| `.env.example` | Environment variable template. |

## Feature Flags

| Flag | Env | Meaning |
| --- | --- | --- |
| `fake_executor` | `SANDBOX_FEATURE_FAKE_EXECUTOR` | Allows the fake executor for local pipeline tests. Must be false in prod. |
| `gojudge_executor` | `SANDBOX_FEATURE_GOJUDGE_EXECUTOR` | Allows the go-judge executor. Must be true outside local fake-only runs. |
| `inline_tests` | `SANDBOX_FEATURE_INLINE_TESTS` | Allows tests to be delivered in Kafka request payloads. |
| `object_storage_tests` | `SANDBOX_FEATURE_OBJECT_STORAGE_TESTS` | Allows tests to be loaded by `suite_id` from object storage. |
| `object_storage_artifacts` | `SANDBOX_FEATURE_OBJECT_STORAGE_ARTIFACTS` | Allows stdout/stderr and later reports to be written to object storage. |

The selected executor is `executor.type`. The corresponding feature flag must also be enabled. For example, `executor.type: gojudge` requires `feature_flags.gojudge_executor: true`.

## Fake Executor

The fake executor is intended for local and contract tests. It does not run untrusted code. It returns a deterministic result from either request metadata or inline file content:

- `metadata.fake_status: success|fail|tle|mle`
- `metadata.fake_result: success|fail|tle|mle`
- file marker such as `fake_status=fail`

The marker may live in code or test files. This lets the full Kafka-to-result pipeline be tested before go-judge is available.

## Environment Mapping

Kafka defaults are aligned with `../infra`:

- host development broker: `localhost:29092`;
- Docker/Kubernetes broker: `kafka:9092`;
- request topic: `code-execution.requests.v1`;
- retry topic: `code-execution.retry.v1`;
- result topic: `code-execution.results.v1`;
- sandbox consumer group: `sandbox-code-execution-v1`.

`code-execution.retry.v1`, `code-execution.events.v1`, and `code-execution.deadletter.v1` are sandbox-side extensions. They must be provisioned before retry/event/DLQ publishing is enabled in a shared environment.

Initial environment variables:

| Env | Config field |
| --- | --- |
| `SANDBOX_CONFIG_FILE` | Config file path loaded by the process. |
| `SANDBOX_ENV` | `app.environment` |
| `SANDBOX_WORKER_ID` | `app.worker_id` |
| `SANDBOX_KAFKA_BROKERS` | `kafka.brokers` |
| `SANDBOX_KAFKA_CLIENT_ID` | `kafka.client_id` |
| `SANDBOX_KAFKA_GROUP_ID` | `kafka.group_id` |
| `SANDBOX_KAFKA_TOPIC_REQUESTS` | `kafka.topics.requests` |
| `SANDBOX_KAFKA_TOPIC_RETRY` | `kafka.topics.retry` |
| `SANDBOX_KAFKA_TOPIC_EVENTS` | `kafka.topics.events` |
| `SANDBOX_KAFKA_TOPIC_RESULTS` | `kafka.topics.results` |
| `SANDBOX_KAFKA_TOPIC_DEADLETTER` | `kafka.topics.deadletter` |
| `SANDBOX_KAFKA_RETRY_ENABLED` | `kafka.retry.enabled` |
| `SANDBOX_KAFKA_RETRY_MAX_ATTEMPTS` | `kafka.retry.max_attempts` |
| `SANDBOX_KAFKA_RETRY_INITIAL_BACKOFF_MS` | `kafka.retry.initial_backoff_ms` |
| `SANDBOX_KAFKA_RETRY_MAX_BACKOFF_MS` | `kafka.retry.max_backoff_ms` |
| `SANDBOX_KAFKA_RETRY_BACKOFF_MULTIPLIER` | `kafka.retry.backoff_multiplier` |
| `SANDBOX_KAFKA_RETRY_EARLY_SLEEP_CAP_MS` | `kafka.retry.early_retry_sleep_cap_ms` |
| `SANDBOX_EXECUTOR_TYPE` | `executor.type` |
| `SANDBOX_GOJUDGE_BASE_URL` | `executor.gojudge.base_url` |
| `SANDBOX_MAX_IN_FLIGHT` | `scheduler.max_in_flight` |
| `SANDBOX_PYTHON_MAX_IN_FLIGHT` | `scheduler.max_in_flight_per_language.python` |
| `SANDBOX_SCHEDULER_JOB_TIMEOUT_MS` | `scheduler.job_timeout_ms` |
| `SANDBOX_SCHEDULER_QUEUE_MAX_DEPTH` | `scheduler.queue.max_depth` |
| `SANDBOX_POSTGRES_DSN` | `storage.postgres.dsn` |
| `SANDBOX_ARTIFACTS_ENABLED` | `storage.artifacts.enabled` |
| `SANDBOX_ARTIFACTS_PROVIDER` | `storage.artifacts.provider` |
| `SANDBOX_ARTIFACTS_BUCKET` | `storage.artifacts.bucket` |
| `SANDBOX_ARTIFACTS_ENDPOINT` | `storage.artifacts.endpoint` |
| `SANDBOX_ARTIFACTS_REGION` | `storage.artifacts.region` |
| `SANDBOX_ARTIFACTS_ACCESS_KEY_ID` | `storage.artifacts.access_key_id` |
| `SANDBOX_ARTIFACTS_SECRET_ACCESS_KEY` | `storage.artifacts.secret_access_key` |
| `SANDBOX_ARTIFACTS_ROOT_DIR` | `storage.artifacts.root_dir` |
| `SANDBOX_ARTIFACTS_KEY_PREFIX` | `storage.artifacts.key_prefix` |
| `SANDBOX_ARTIFACTS_INLINE_MAX_BYTES` | `storage.artifacts.inline_max_bytes` |
| `SANDBOX_ARTIFACTS_MAX_BYTES` | `storage.artifacts.max_bytes` |
| `SANDBOX_ARTIFACTS_REQUEST_TIMEOUT_MS` | `storage.artifacts.request_timeout_ms` |
| `SANDBOX_ARTIFACTS_FORCE_PATH_STYLE` | `storage.artifacts.force_path_style` |
| `SANDBOX_TESTS_BUCKET` | `tests.object_storage.bucket` |
| `SANDBOX_TESTS_ENDPOINT` | `tests.object_storage.endpoint` |
| `SANDBOX_TESTS_REGION` | `tests.object_storage.region` |
| `SANDBOX_TESTS_ACCESS_KEY_ID` | `tests.object_storage.access_key_id` |
| `SANDBOX_TESTS_SECRET_ACCESS_KEY` | `tests.object_storage.secret_access_key` |
| `SANDBOX_TESTS_KEY_PREFIX` | `tests.object_storage.key_prefix` |
| `SANDBOX_TESTS_CACHE_TTL_MS` | `tests.object_storage.cache_ttl_ms` |
| `SANDBOX_TESTS_OBJECT_MAX_BYTES` | `tests.object_storage.max_bytes` |
| `SANDBOX_TESTS_REQUEST_TIMEOUT_MS` | `tests.object_storage.request_timeout_ms` |
| `SANDBOX_TESTS_FORCE_PATH_STYLE` | `tests.object_storage.force_path_style` |

## Artifact Storage

`storage.artifacts` controls result output artifacts. When enabled, stdout and
stderr larger than `inline_max_bytes` are uploaded to the configured provider and
the Kafka `RunResult.logs` fields keep only an inline preview plus an artifact
reference.

Supported artifact providers:

- `filesystem`: local development provider. Object keys are guarded against
  absolute paths and `..` traversal before writing under `root_dir`.
- `s3`: S3-compatible provider using SigV4 request signing. For MinIO and most
  local endpoints set `force_path_style: true`.

`max_bytes` caps the bytes uploaded per log stream. If this cap is hit,
`logs.truncated` and the artifact reference `truncated` flag are set.

Local Compose stores artifacts on a filesystem volume by default. The
`object-storage` Compose profile starts MinIO and creates the artifacts/tests
buckets; switch `SANDBOX_ARTIFACTS_PROVIDER=s3` and point
`SANDBOX_ARTIFACTS_ENDPOINT` at MinIO to exercise the S3 path.

## Object Storage Test Suites

When `tests.object_storage.enabled=true`, requests may provide
`tests.source=object_storage` with required `suite_id`, optional `version`, and
required lowercase `checksum_sha256`. Kafka carries only this reference and
checksum; hidden test files stay in object storage. The worker loads a JSON
suite manifest from:

`<key_prefix>/test-suites/<suite_id>/<version-or-latest>.json`

The manifest schema version is `sandbox.testsuite.v1`. The loaded files are
validated again before execution, then materialized as inline test files for the
language adapter. Fetch failures are retryable infrastructure errors; malformed
suites and checksum mismatches become terminal `invalid_request` results.

## Safety Defaults

The config defaults are intentionally restrictive:

- User code network access is disabled.
- Metadata endpoints are denied.
- Absolute paths and `..` path segments are rejected.
- Symlinks are rejected.
- Output is capped and truncated.
- Kafka commits happen only after result publication.
- Kafka producer idempotency is enabled.

Security-sensitive values from requests must be clamped to `configs/limits.yaml`.
