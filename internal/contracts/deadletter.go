package contracts

import "time"

const RunDeadLetterSchemaVersion = "sandbox.run.deadletter.v1"

type RunDeadLetter struct {
	SchemaVersion      string            `json:"schema_version"`
	SubmissionID       string            `json:"submission_id,omitempty"`
	AttemptID          string            `json:"attempt_id,omitempty"`
	Reason             string            `json:"reason"`
	Message            string            `json:"message,omitempty"`
	Retryable          bool              `json:"retryable"`
	RetryCount         int               `json:"retry_count"`
	OriginalTopic      string            `json:"original_topic"`
	OriginalPartition  int               `json:"original_partition"`
	OriginalOffset     int64             `json:"original_offset"`
	OriginalKey        *string           `json:"original_key"`
	OriginalHeaders    map[string]string `json:"original_headers"`
	OriginalPayloadB64 *string           `json:"original_payload_b64"`
	TraceID            string            `json:"trace_id,omitempty"`
	WorkerID           string            `json:"worker_id,omitempty"`
	FailedAt           time.Time         `json:"failed_at"`
}
