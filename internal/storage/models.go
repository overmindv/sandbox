package storage

import (
	"encoding/json"
	"time"
)

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusCancelled RunStatus = "cancelled"
)

type AttemptStatus string

const (
	AttemptStatusRunning        AttemptStatus = "running"
	AttemptStatusCompleted      AttemptStatus = "completed"
	AttemptStatusFailedInternal AttemptStatus = "failed_internal"
	AttemptStatusCancelled      AttemptStatus = "cancelled"
	AttemptStatusExpired        AttemptStatus = "expired"
)

type WorkerStatus string

const (
	WorkerStatusStarting WorkerStatus = "starting"
	WorkerStatusActive   WorkerStatus = "active"
	WorkerStatusStopping WorkerStatus = "stopping"
	WorkerStatusStopped  WorkerStatus = "stopped"
)

type RunFileKind string

const (
	RunFileKindSource RunFileKind = "source"
	RunFileKindTest   RunFileKind = "test"
)

type ArtifactKind string

const (
	ArtifactKindStdout     ArtifactKind = "stdout"
	ArtifactKindStderr     ArtifactKind = "stderr"
	ArtifactKindCompileLog ArtifactKind = "compile_log"
	ArtifactKindPytestJSON ArtifactKind = "pytest_json"
	ArtifactKindRunArchive ArtifactKind = "run_archive"
)

type ArtifactStorageProvider string

const (
	ArtifactStoragePostgresJSON ArtifactStorageProvider = "postgres_json"
	ArtifactStorageS3           ArtifactStorageProvider = "s3"
	ArtifactStorageFilesystem   ArtifactStorageProvider = "filesystem"
)

type OutboxEventType string

const (
	OutboxEventRunResultReady  OutboxEventType = "run_result_ready"
	OutboxEventRunEventReady   OutboxEventType = "run_event_ready"
	OutboxEventRetryReady      OutboxEventType = "retry_ready"
	OutboxEventDeadLetterReady OutboxEventType = "deadletter_ready"
)

type OutboxStatus string

const (
	OutboxStatusPending   OutboxStatus = "pending"
	OutboxStatusPublished OutboxStatus = "published"
	OutboxStatusFailed    OutboxStatus = "failed"
)

type RunRecord struct {
	RunID           string
	SubmissionID    string
	UserID          string
	TaskID          string
	TaskVersionID   string
	LanguageID      string
	LanguageVersion string
	Status          RunStatus
	RequestHash     string
	LimitsProfileID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

type AttemptRecord struct {
	AttemptID        string
	RunID            string
	SubmissionID     string
	AttemptNo        int
	Status           AttemptStatus
	RequestHash      string
	RequestJSON      json.RawMessage
	ResultJSON       json.RawMessage
	WorkerInstanceID string
	StartedAt        time.Time
	CompletedAt      *time.Time
	LeaseExpiresAt   *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type IdempotencyKeyRecord struct {
	Key         string
	RunID       string
	AttemptID   string
	RequestHash string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
}

type WorkerInstanceRecord struct {
	WorkerInstanceID string
	Hostname         string
	Environment      string
	ExecutorType     string
	Version          string
	Status           WorkerStatus
	StartedAt        time.Time
	LastSeenAt       time.Time
	StoppedAt        *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type WorkerCapabilityRecord struct {
	WorkerInstanceID string
	LanguageID       string
	LanguageVersion  string
	MaxConcurrency   int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RunFileRecord struct {
	FileID      string
	RunID       string
	Kind        RunFileKind
	Path        string
	ContentHash string
	ObjectKey   string
	SizeBytes   int64
	CreatedAt   time.Time
}

type RunTestRecord struct {
	RunTestID  string
	RunID      string
	TestRefID  string
	Name       string
	Source     string
	Mode       string
	OrderIndex int
	TestJSON   json.RawMessage
	CreatedAt  time.Time
}

type AttemptTestResultRecord struct {
	AttemptTestResultID string
	AttemptID           string
	RunTestID           string
	OrderIndex          int
	Name                string
	Status              string
	DurationMS          int
	Message             string
	StdoutArtifactID    string
	StderrArtifactID    string
	ErrorJSON           json.RawMessage
	CreatedAt           time.Time
}

type ArtifactRecord struct {
	ArtifactID      string
	RunID           string
	AttemptID       string
	Kind            ArtifactKind
	StorageProvider ArtifactStorageProvider
	ObjectKey       string
	ContentHash     string
	SizeBytes       int64
	Truncated       bool
	CreatedAt       time.Time
}

type OutboxEventRecord struct {
	EventID         string
	AggregateType   string
	AggregateID     string
	RunID           string
	AttemptID       string
	EventType       OutboxEventType
	Topic           string
	Key             string
	PayloadJSON     json.RawMessage
	HeadersJSON     json.RawMessage
	Status          OutboxStatus
	PublishAttempts int
	AvailableAt     time.Time
	PublishedAt     *time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type OutboxFilter struct {
	Status OutboxStatus
	Limit  int
}
