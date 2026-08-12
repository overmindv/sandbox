package contracts

import "time"

const RunEventSchemaVersion = "sandbox.run.event.v1"

type EventType string

const (
	EventAccepted  EventType = "accepted"
	EventQueued    EventType = "queued"
	EventStarted   EventType = "started"
	EventPrepared  EventType = "prepared"
	EventCompleted EventType = "completed"
	EventFailed    EventType = "failed"
	EventRejected  EventType = "rejected"
	EventRetried   EventType = "retried"
)

func (t EventType) Valid() bool {
	switch t {
	case EventAccepted,
		EventQueued,
		EventStarted,
		EventPrepared,
		EventCompleted,
		EventFailed,
		EventRejected,
		EventRetried:
		return true
	default:
		return false
	}
}

type RunEvent struct {
	SchemaVersion string          `json:"schema_version"`
	SubmissionID  string          `json:"submission_id"`
	AttemptID     string          `json:"attempt_id"`
	EventType     EventType       `json:"event_type"`
	Status        ExecutionStatus `json:"status"`
	Message       string          `json:"message,omitempty"`
	Timing        *EventTiming    `json:"timing,omitempty"`
	TraceID       string          `json:"trace_id,omitempty"`
	WorkerID      string          `json:"worker_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type EventTiming struct {
	QueueMS   int `json:"queue_ms,omitempty"`
	ElapsedMS int `json:"elapsed_ms,omitempty"`
}
