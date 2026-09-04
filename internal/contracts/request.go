package contracts

import "time"

const (
	RunRequestSchemaVersion = "sandbox.run.request.v1"
	// ExecutionMode включает режим чистого исполнения: код прогоняется по каждому
	// входному кейсу, возвращается вывод и мета-данные без сравнения с эталоном.
	ExecutionMode = "execution"
)

type RunRequest struct {
	SchemaVersion string         `json:"schema_version"`
	SubmissionID  string         `json:"submission_id"`
	AttemptID     string         `json:"attempt_id"`
	UserID        string         `json:"user_id"`
	TaskID        string         `json:"task_id"`
	Language      LanguageSpec   `json:"language"`
	Code          CodeSpec       `json:"code"`
	Tests         TestSpec       `json:"tests,omitempty"`
	Execution     *ExecutionSpec `json:"execution,omitempty"`
	Limits        Limits         `json:"limits"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	TraceID       string         `json:"trace_id"`
	CreatedAt     time.Time      `json:"created_at"`
}

// ExecutionSpec описывает режим чистого исполнения: список входных кейсов,
// которые подаются в stdin программы по одному на запуск.
type ExecutionSpec struct {
	Mode   string   `json:"mode"`
	Inputs []string `json:"inputs"`
}

type LanguageSpec struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type CodeSpec struct {
	Entrypoint string       `json:"entrypoint,omitempty"`
	Files      []SourceFile `json:"files"`
}

type SourceFile struct {
	Path           string `json:"path"`
	ContentB64     string `json:"content_b64"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
}

type TestSource string

const (
	TestSourceInline        TestSource = "inline"
	TestSourceObjectStorage TestSource = "object_storage"
)

type TestMode string

const (
	TestModePytest TestMode = "pytest"
	TestModeGoTest TestMode = "go-test"
)

type TestSpec struct {
	Mode           TestMode     `json:"mode"`
	Source         TestSource   `json:"source"`
	Entrypoint     string       `json:"entrypoint,omitempty"`
	Files          []SourceFile `json:"files,omitempty"`
	SuiteID        string       `json:"suite_id,omitempty"`
	Version        string       `json:"version,omitempty"`
	ChecksumSHA256 string       `json:"checksum_sha256,omitempty"`
}

type Limits struct {
	CPUms          int `json:"cpu_ms"`
	Wallms         int `json:"wall_ms"`
	MemoryMB       int `json:"memory_mb"`
	PIDs           int `json:"pids"`
	StdoutBytes    int `json:"stdout_bytes"`
	StderrBytes    int `json:"stderr_bytes"`
	WorkspaceBytes int `json:"workspace_bytes"`
}
