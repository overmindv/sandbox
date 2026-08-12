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
- result topic: `code-execution.results.v1`;
- sandbox consumer group: `sandbox-code-execution-v1`.

`code-execution.events.v1` and `code-execution.deadletter.v1` are currently sandbox-side extensions. Infra creates only request/result topics today, so those topics must be provisioned before event/DLQ publishing is enabled in a shared environment.

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
| `SANDBOX_KAFKA_TOPIC_EVENTS` | `kafka.topics.events` |
| `SANDBOX_KAFKA_TOPIC_RESULTS` | `kafka.topics.results` |
| `SANDBOX_KAFKA_TOPIC_DEADLETTER` | `kafka.topics.deadletter` |
| `SANDBOX_EXECUTOR_TYPE` | `executor.type` |
| `SANDBOX_GOJUDGE_BASE_URL` | `executor.gojudge.base_url` |
| `SANDBOX_MAX_IN_FLIGHT` | `scheduler.max_in_flight` |
| `SANDBOX_PYTHON_MAX_IN_FLIGHT` | `scheduler.max_in_flight_per_language.python` |
| `SANDBOX_SCHEDULER_JOB_TIMEOUT_MS` | `scheduler.job_timeout_ms` |
| `SANDBOX_SCHEDULER_QUEUE_MAX_DEPTH` | `scheduler.queue.max_depth` |
| `SANDBOX_POSTGRES_DSN` | `storage.postgres.dsn` |
| `SANDBOX_TESTS_BUCKET` | `tests.object_storage.bucket` |
| `SANDBOX_TESTS_ENDPOINT` | `tests.object_storage.endpoint` |
| `SANDBOX_TESTS_REGION` | `tests.object_storage.region` |

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
