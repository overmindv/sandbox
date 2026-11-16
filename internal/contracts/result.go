package contracts

import "time"

const RunResultSchemaVersion = "sandbox.run.result.v1"

type RunResult struct {
	DiagnosticsVisibility string                `json:"diagnostics_visibility,omitempty"`
	EvaluationContext     *EvaluationContext    `json:"evaluation_context,omitempty"`
	SchemaVersion         string                `json:"schema_version"`
	SubmissionID          string                `json:"submission_id"`
	AttemptID             string                `json:"attempt_id"`
	UserID                string                `json:"user_id,omitempty"`
	TaskID                string                `json:"task_id,omitempty"`
	Language              LanguageSpec          `json:"language,omitempty"`
	Status                ExecutionStatus       `json:"status"`
	Summary               ResultSummary         `json:"summary"`
	Timing                ResultTiming          `json:"timing"`
	Resources             ResultResources       `json:"resources"`
	Logs                  ResultLogs            `json:"logs"`
	Tests                 []TestCaseResult      `json:"tests,omitempty"`
	Cases                 []CaseExecutionResult `json:"cases,omitempty"`
	Error                 *RunError             `json:"error"`
	TraceID               string                `json:"trace_id,omitempty"`
	WorkerID              string                `json:"worker_id,omitempty"`
	CreatedAt             time.Time             `json:"created_at"`
}

// CaseExecutionResult описывает результат исполнения одного входного кейса в
// execution-режиме: вывод, мета-данные и статус исполнения без сравнения с эталоном.
type CaseExecutionResult struct {
	DiagnosticsVisibility string  `json:"diagnostics_visibility,omitempty"`
	Index                 int     `json:"index"`
	Stdout                string  `json:"stdout"`
	Stderr                string  `json:"stderr"`
	Truncated             bool    `json:"truncated"`
	ExitCode              *int    `json:"exit_code"`
	Signal                *string `json:"signal"`
	CPUms                 int     `json:"cpu_ms,omitempty"`
	MemoryPeakBytes       int64   `json:"memory_peak_bytes,omitempty"`
	DurationMS            int     `json:"duration_ms,omitempty"`
	Status                string  `json:"status"`
}

type ResultSummary struct {
	CasesTotal      int    `json:"cases_total,omitempty"`
	CasesWithError  int    `json:"cases_with_error,omitempty"`
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
	Stdout          string             `json:"stdout"`
	Stderr          string             `json:"stderr"`
	Truncated       bool               `json:"truncated"`
	InlineTruncated bool               `json:"inline_truncated,omitempty"`
	StdoutArtifact  *ArtifactReference `json:"stdout_artifact,omitempty"`
	StderrArtifact  *ArtifactReference `json:"stderr_artifact,omitempty"`
}

type ArtifactReference struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Provider    string `json:"provider"`
	Bucket      string `json:"bucket,omitempty"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	Truncated   bool   `json:"truncated"`
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
