package contracts

import "time"

const RunResultSchemaVersion = "sandbox.run.result.v1"

type RunResult struct {
	SchemaVersion string           `json:"schema_version"`
	SubmissionID  string           `json:"submission_id"`
	AttemptID     string           `json:"attempt_id"`
	UserID        string           `json:"user_id,omitempty"`
	TaskID        string           `json:"task_id,omitempty"`
	Language      LanguageSpec     `json:"language,omitempty"`
	Status        ExecutionStatus  `json:"status"`
	Summary       ResultSummary    `json:"summary"`
	Timing        ResultTiming     `json:"timing"`
	Resources     ResultResources  `json:"resources"`
	Logs          ResultLogs       `json:"logs"`
	Tests         []TestCaseResult `json:"tests,omitempty"`
	Error         *RunError        `json:"error"`
	TraceID       string           `json:"trace_id,omitempty"`
	WorkerID      string           `json:"worker_id,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
}

type ResultSummary struct {
	TestsTotal      int    `json:"tests_total"`
	TestsPassed     int    `json:"tests_passed"`
	TestsFailed     int    `json:"tests_failed"`
	FirstFailedTest string `json:"first_failed_test,omitempty"`
}

type ResultTiming struct {
	QueueMS   int `json:"queue_ms"`
	PrepareMS int `json:"prepare_ms"`
	RunMS     int `json:"run_ms"`
	TotalMS   int `json:"total_ms"`
}

type ResultResources struct {
	CPUms           int     `json:"cpu_ms"`
	MemoryPeakBytes int64   `json:"memory_peak_bytes"`
	ExitCode        *int    `json:"exit_code"`
	Signal          *string `json:"signal"`
}

type ResultLogs struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

type TestCaseResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int    `json:"duration_ms,omitempty"`
	Message    string `json:"message,omitempty"`
}

type RunError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}
