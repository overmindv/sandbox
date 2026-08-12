package execution

import (
	"encoding/base64"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
)

type FileKind string

const (
	FileKindSource FileKind = "source"
	FileKindTest   FileKind = "test"
)

type ExecutionCommand struct {
	ID           string
	SubmissionID string
	AttemptID    string
	UserID       string
	TaskID       string
	Language     contracts.LanguageSpec
	Entrypoint   string
	TestMode     contracts.TestMode
	TestSource   contracts.TestSource
	Files        []ExecutionFile
	Limits       contracts.Limits
	Metadata     map[string]any
	TraceID      string
	CreatedAt    time.Time
}

type ExecutionFile struct {
	Path    string
	Kind    FileKind
	Content []byte
}

func CommandFromRunRequest(req contracts.RunRequest) (ExecutionCommand, error) {
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
		ID:           req.AttemptID,
		SubmissionID: req.SubmissionID,
		AttemptID:    req.AttemptID,
		UserID:       req.UserID,
		TaskID:       req.TaskID,
		Language:     req.Language,
		Entrypoint:   req.Code.Entrypoint,
		TestMode:     req.Tests.Mode,
		TestSource:   req.Tests.Source,
		Files:        files,
		Limits:       req.Limits,
		Metadata:     req.Metadata,
		TraceID:      req.TraceID,
		CreatedAt:    req.CreatedAt,
	}, nil
}

func decodeContent(contentB64 string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}
