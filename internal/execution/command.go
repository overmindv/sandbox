package execution

import (
	"encoding/base64"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
)

type FileKind string

const (
	FileKindSource  FileKind = "source"
	FileKindTest    FileKind = "test"
	FileKindHarness FileKind = "harness"
)

type ExecutionCommand struct {
	ID             string
	SubmissionID   string
	AttemptID      string
	UserID         string
	TaskID         string
	Language       contracts.LanguageSpec
	Adapter        string
	RuntimeImage   string
	WorkingDir     string
	Argv           []string
	CompileArgv    []string
	Environment    map[string]string
	CopyOut        []string
	Entrypoint     string
	TestEntrypoint string
	TestMode       contracts.TestMode
	TestSource     contracts.TestSource
	// Mode задаёт режим исполнения: "execution" (чистый прогон по входам) или пусто (pytest/go-test).
	Mode          string
	Inputs        []string
	Files         []ExecutionFile
	Limits        contracts.Limits
	RuntimeLimits RuntimeLimits
	Metadata      map[string]any
	TraceID       string
	CreatedAt     time.Time
}

type RuntimeLimits struct {
	CPUms          int
	WallMS         int
	MemoryBytes    int64
	PIDs           int
	StdoutBytes    int
	StderrBytes    int
	WorkspaceBytes int64
}

type ExecutionFile struct {
	Path    string
	Kind    FileKind
	Content []byte
}

// IsExecutionMode сообщает, что команда исполняется в режиме чистого прогона.
func IsExecutionMode(command ExecutionCommand) bool {
	return command.Mode == contracts.ExecutionMode
}

func CommandFromRunRequest(req contracts.RunRequest) (ExecutionCommand, error) {
	mode := ""
	var inputs []string
	if req.Execution != nil {
		mode = req.Execution.Mode
		inputs = req.Execution.Inputs
	}
	files := make([]ExecutionFile, 0, len(req.Code.Files)+len(req.Tests.Files))
	for _, file := range req.Code.Files {
		decoded, err := decodeContent(file.ContentB64)
		if err != nil {
			return ExecutionCommand{}, fmt.Errorf("decode code file %q: %w", file.Path, err)
		}
		files = append(files, ExecutionFile{
			Path:    file.Path,
			Kind:    FileKindSource,
			Content: decoded,
		})
	}
	for _, file := range req.Tests.Files {
		decoded, err := decodeContent(file.ContentB64)
		if err != nil {
			return ExecutionCommand{}, fmt.Errorf("decode test file %q: %w", file.Path, err)
		}
		files = append(files, ExecutionFile{
			Path:    file.Path,
			Kind:    FileKindTest,
			Content: decoded,
		})
	}

	return ExecutionCommand{
		ID:             req.AttemptID,
		SubmissionID:   req.SubmissionID,
		AttemptID:      req.AttemptID,
		UserID:         req.UserID,
		TaskID:         req.TaskID,
		Language:       req.Language,
		Entrypoint:     req.Code.Entrypoint,
		TestEntrypoint: req.Tests.Entrypoint,
		TestMode:       req.Tests.Mode,
		TestSource:     req.Tests.Source,
		Mode:           mode,
		Inputs:         inputs,
		Files:          files,
		Limits:         req.Limits,
		RuntimeLimits:  RuntimeLimitsFromRequest(req.Limits),
		Metadata:       req.Metadata,
		TraceID:        req.TraceID,
		CreatedAt:      req.CreatedAt,
	}, nil
}

func RuntimeLimitsFromRequest(limits contracts.Limits) RuntimeLimits {
	return RuntimeLimits{
		CPUms:          limits.CPUms,
		WallMS:         limits.Wallms,
		MemoryBytes:    int64(limits.MemoryMB) * 1024 * 1024,
		PIDs:           limits.PIDs,
		StdoutBytes:    limits.StdoutBytes,
		StderrBytes:    limits.StderrBytes,
		WorkspaceBytes: int64(limits.WorkspaceBytes),
	}
}

func decodeContent(contentB64 string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}
