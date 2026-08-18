CREATE TABLE IF NOT EXISTS worker_instances (
    worker_instance_id TEXT PRIMARY KEY,
    hostname TEXT,
    environment TEXT,
    executor_type TEXT,
    version TEXT,
    status TEXT NOT NULL CHECK (status IN ('starting', 'active', 'stopping', 'stopped')),
    started_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    stopped_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS worker_instances_status_idx ON worker_instances(status);
CREATE INDEX IF NOT EXISTS worker_instances_last_seen_at_idx ON worker_instances(last_seen_at);

CREATE TABLE IF NOT EXISTS worker_capabilities (
    worker_instance_id TEXT NOT NULL REFERENCES worker_instances(worker_instance_id) ON DELETE CASCADE,
    language_id TEXT NOT NULL,
    language_version TEXT NOT NULL,
    max_concurrency INTEGER NOT NULL CHECK (max_concurrency > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (worker_instance_id, language_id, language_version)
);

CREATE INDEX IF NOT EXISTS worker_capabilities_language_idx ON worker_capabilities(language_id, language_version);

CREATE TABLE IF NOT EXISTS runs (
    run_id UUID PRIMARY KEY,
    submission_id UUID NOT NULL,
    user_id UUID NOT NULL,
    task_id UUID NOT NULL,
    task_version_id UUID,
    language_id TEXT NOT NULL,
    language_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'cancelled')),
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
    limits_profile_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (submission_id, request_hash)
);

CREATE INDEX IF NOT EXISTS runs_submission_id_idx ON runs(submission_id);
CREATE INDEX IF NOT EXISTS runs_user_task_idx ON runs(user_id, task_id);
CREATE INDEX IF NOT EXISTS runs_status_updated_at_idx ON runs(status, updated_at);
CREATE INDEX IF NOT EXISTS runs_completed_at_idx ON runs(completed_at);

CREATE TABLE IF NOT EXISTS attempts (
    attempt_id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    submission_id UUID NOT NULL,
    attempt_no INTEGER NOT NULL CHECK (attempt_no > 0),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed_internal', 'cancelled', 'expired')),
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
    request_json JSONB NOT NULL,
    result_json JSONB,
    worker_instance_id TEXT REFERENCES worker_instances(worker_instance_id) ON DELETE SET NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, attempt_no)
);

CREATE INDEX IF NOT EXISTS attempts_run_id_idx ON attempts(run_id);
CREATE INDEX IF NOT EXISTS attempts_submission_id_idx ON attempts(submission_id);
CREATE INDEX IF NOT EXISTS attempts_status_idx ON attempts(status);
CREATE INDEX IF NOT EXISTS attempts_worker_status_idx ON attempts(worker_instance_id, status);
CREATE INDEX IF NOT EXISTS attempts_completed_at_idx ON attempts(completed_at);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    idempotency_key TEXT PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    attempt_id UUID NOT NULL REFERENCES attempts(attempt_id) ON DELETE CASCADE,
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idempotency_keys_run_id_idx ON idempotency_keys(run_id);
CREATE INDEX IF NOT EXISTS idempotency_keys_attempt_id_idx ON idempotency_keys(attempt_id);
CREATE INDEX IF NOT EXISTS idempotency_keys_expires_at_idx ON idempotency_keys(expires_at);

CREATE TABLE IF NOT EXISTS run_files (
    file_id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('source', 'test')),
    path TEXT NOT NULL,
    content_hash TEXT NOT NULL CHECK (length(content_hash) = 64),
    object_key TEXT,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, kind, path)
);

CREATE INDEX IF NOT EXISTS run_files_run_kind_idx ON run_files(run_id, kind);
CREATE INDEX IF NOT EXISTS run_files_content_hash_idx ON run_files(content_hash);

CREATE TABLE IF NOT EXISTS run_tests (
    run_test_id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    test_ref_id TEXT,
    name TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('inline', 'object_storage')),
    mode TEXT NOT NULL,
    order_index INTEGER NOT NULL CHECK (order_index >= 0),
    test_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, order_index)
);

CREATE INDEX IF NOT EXISTS run_tests_run_id_idx ON run_tests(run_id);
CREATE INDEX IF NOT EXISTS run_tests_test_ref_id_idx ON run_tests(test_ref_id);

CREATE TABLE IF NOT EXISTS artifacts (
    artifact_id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    attempt_id UUID REFERENCES attempts(attempt_id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('stdout', 'stderr', 'compile_log', 'pytest_json', 'run_archive')),
    storage_provider TEXT NOT NULL CHECK (storage_provider IN ('postgres_json', 's3', 'filesystem')),
    object_key TEXT,
    content_hash TEXT NOT NULL CHECK (length(content_hash) = 64),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    truncated BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS artifacts_run_id_idx ON artifacts(run_id);
CREATE INDEX IF NOT EXISTS artifacts_attempt_id_idx ON artifacts(attempt_id);
CREATE INDEX IF NOT EXISTS artifacts_kind_idx ON artifacts(kind);
CREATE INDEX IF NOT EXISTS artifacts_created_at_idx ON artifacts(created_at);

CREATE TABLE IF NOT EXISTS attempt_test_results (
    attempt_test_result_id UUID PRIMARY KEY,
    attempt_id UUID NOT NULL REFERENCES attempts(attempt_id) ON DELETE CASCADE,
    run_test_id UUID REFERENCES run_tests(run_test_id) ON DELETE SET NULL,
    order_index INTEGER NOT NULL CHECK (order_index >= 0),
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    message TEXT,
    stdout_artifact_id UUID REFERENCES artifacts(artifact_id) ON DELETE SET NULL,
    stderr_artifact_id UUID REFERENCES artifacts(artifact_id) ON DELETE SET NULL,
    error_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (attempt_id, order_index)
);

CREATE INDEX IF NOT EXISTS attempt_test_results_attempt_id_idx ON attempt_test_results(attempt_id);
CREATE INDEX IF NOT EXISTS attempt_test_results_status_idx ON attempt_test_results(status);

CREATE TABLE IF NOT EXISTS outbox_events (
    event_id UUID PRIMARY KEY,
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    run_id UUID NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    attempt_id UUID REFERENCES attempts(attempt_id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('run_result_ready', 'run_event_ready', 'retry_ready', 'deadletter_ready')),
    topic TEXT,
    message_key TEXT,
    payload_json JSONB NOT NULL,
    headers_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL CHECK (status IN ('pending', 'published', 'failed')),
    publish_attempts INTEGER NOT NULL DEFAULT 0 CHECK (publish_attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (aggregate_type, aggregate_id, event_type)
);

CREATE INDEX IF NOT EXISTS outbox_events_pending_idx ON outbox_events(status, available_at, event_id);
CREATE INDEX IF NOT EXISTS outbox_events_run_id_idx ON outbox_events(run_id);
CREATE INDEX IF NOT EXISTS outbox_events_attempt_id_idx ON outbox_events(attempt_id);
CREATE INDEX IF NOT EXISTS outbox_events_created_at_idx ON outbox_events(created_at);
