CREATE TABLE IF NOT EXISTS runs (
    submission_id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    task_id UUID NOT NULL,
    language_id TEXT NOT NULL,
    language_version TEXT NOT NULL,
    first_attempt_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS attempts (
    attempt_id UUID PRIMARY KEY,
    submission_id UUID NOT NULL REFERENCES runs(submission_id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed')),
    request_json JSONB NOT NULL,
    result_json JSONB,
    worker_id TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS attempts_submission_id_idx ON attempts(submission_id);
CREATE INDEX IF NOT EXISTS attempts_status_idx ON attempts(status);
CREATE INDEX IF NOT EXISTS attempts_completed_at_idx ON attempts(completed_at);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    idempotency_key TEXT PRIMARY KEY,
    attempt_id UUID NOT NULL REFERENCES attempts(attempt_id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idempotency_keys_attempt_id_idx ON idempotency_keys(attempt_id);
