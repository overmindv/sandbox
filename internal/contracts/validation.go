package contracts

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

var (
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	languageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	filePathPattern   = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	checksumPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type ValidationOptions struct {
	MaxSourceFiles     int
	MaxTestFiles       int
	MaxFileContentByte int
	MaxPathLength      int
	MaxMetadataKeys    int
	HardMaxLimits      Limits
	MinLimits          Limits
}

func DefaultValidationOptions() ValidationOptions {
	return ValidationOptions{
		MaxSourceFiles:     32,
		MaxTestFiles:       32,
		MaxFileContentByte: 1048576,
		MaxPathLength:      128,
		MaxMetadataKeys:    32,
		MinLimits: Limits{
			CPUms:          100,
			Wallms:         100,
			MemoryMB:       16,
			PIDs:           1,
			StdoutBytes:    0,
			StderrBytes:    0,
			WorkspaceBytes: 1024,
		},
		HardMaxLimits: Limits{
			CPUms:          30000,
			Wallms:         60000,
			MemoryMB:       1024,
			PIDs:           256,
			StdoutBytes:    1048576,
			StderrBytes:    1048576,
			WorkspaceBytes: 10485760,
		},
	}
}

type ValidationError struct {
	Field   string
	Message string
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("validation failed")
	for _, item := range e {
		b.WriteString("; ")
		b.WriteString(item.Field)
		b.WriteString(": ")
		b.WriteString(item.Message)
	}
	return b.String()
}

func (e *ValidationErrors) add(field, message string) {
	*e = append(*e, ValidationError{Field: field, Message: message})
}

func ValidateRunRequest(req RunRequest) error {
	return ValidateRunRequestWithOptions(req, DefaultValidationOptions())
}

func ValidateRunRequestWithOptions(req RunRequest, opts ValidationOptions) error {
	var errs ValidationErrors

	if req.SchemaVersion != RunRequestSchemaVersion {
		errs.add("schema_version", fmt.Sprintf("must be %q", RunRequestSchemaVersion))
	}
	validateUUID(&errs, "submission_id", req.SubmissionID)
	validateUUID(&errs, "attempt_id", req.AttemptID)
	validateUUID(&errs, "user_id", req.UserID)
	validateUUID(&errs, "task_id", req.TaskID)

	if !languageIDPattern.MatchString(req.Language.ID) {
		errs.add("language.id", "must match ^[a-z][a-z0-9_-]{0,31}$")
	}
	if req.Language.Version == "" || len(req.Language.Version) > 32 {
		errs.add("language.version", "must be 1-32 characters")
	}

	if req.Code.Entrypoint != "" {
		validateSafePath(&errs, "code.entrypoint", req.Code.Entrypoint, opts.MaxPathLength)
	}
	validateFiles(&errs, "code.files", req.Code.Files, opts.MaxSourceFiles, opts)
	validateTests(&errs, req.Tests, opts)
	validateLimits(&errs, req.Limits, opts)

	if len(req.Metadata) > opts.MaxMetadataKeys {
		errs.add("metadata", fmt.Sprintf("must contain at most %d keys", opts.MaxMetadataKeys))
	}
	for key, value := range req.Metadata {
		if key == "" || len(key) > 64 {
			errs.add("metadata."+key, "key must be 1-64 characters")
		}
		switch value.(type) {
		case string, float64, int, int64, bool, nil:
		default:
			errs.add("metadata."+key, "value must be scalar")
		}
	}

	if req.TraceID == "" || len(req.TraceID) > 128 {
		errs.add("trace_id", "must be 1-128 characters")
	}
	if req.CreatedAt.IsZero() {
		errs.add("created_at", "must be set")
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateUUID(errs *ValidationErrors, field, value string) {
	if !uuidPattern.MatchString(value) {
		errs.add(field, "must be a UUID")
	}
}

func validateTests(errs *ValidationErrors, tests TestSpec, opts ValidationOptions) {
	if tests.Mode != TestModePytest {
		errs.add("tests.mode", "must be pytest")
	}

	switch tests.Source {
	case TestSourceInline:
		if tests.Entrypoint != "" {
			validateSafePath(errs, "tests.entrypoint", tests.Entrypoint, opts.MaxPathLength)
		}
		validateFiles(errs, "tests.files", tests.Files, opts.MaxTestFiles, opts)
		if tests.SuiteID != "" || tests.ChecksumSHA256 != "" {
			errs.add("tests", "inline tests must not set suite_id or checksum_sha256")
		}
	case TestSourceObjectStorage:
		if len(tests.Files) > 0 {
			errs.add("tests.files", "object storage tests must not include inline files")
		}
		if tests.SuiteID == "" || len(tests.SuiteID) > 128 {
			errs.add("tests.suite_id", "must be 1-128 characters")
		}
		if !checksumPattern.MatchString(tests.ChecksumSHA256) {
			errs.add("tests.checksum_sha256", "must be a lowercase sha256 hex digest")
		}
	default:
		errs.add("tests.source", "must be inline or object_storage")
	}
}

func validateFiles(errs *ValidationErrors, field string, files []SourceFile, maxFiles int, opts ValidationOptions) {
	if len(files) == 0 {
		errs.add(field, "must contain at least one file")
		return
	}
	if len(files) > maxFiles {
		errs.add(field, fmt.Sprintf("must contain at most %d files", maxFiles))
	}

	seen := make(map[string]struct{}, len(files))
	for i, file := range files {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		validateSafePath(errs, prefix+".path", file.Path, opts.MaxPathLength)
		if _, exists := seen[file.Path]; exists {
			errs.add(prefix+".path", "duplicate file path")
		}
		seen[file.Path] = struct{}{}

		validateBase64Content(errs, prefix+".content_b64", file.ContentB64, opts.MaxFileContentByte)
		if file.ChecksumSHA256 != "" && !checksumPattern.MatchString(file.ChecksumSHA256) {
			errs.add(prefix+".checksum_sha256", "must be a lowercase sha256 hex digest")
		}
	}
}

func validateSafePath(errs *ValidationErrors, field, path string, maxLength int) {
	if path == "" {
		errs.add(field, "must not be empty")
		return
	}
	if len(path) > maxLength {
		errs.add(field, fmt.Sprintf("must be at most %d characters", maxLength))
	}
	if strings.Contains(path, "\\") {
		errs.add(field, "must use forward slashes")
	}
	if strings.HasPrefix(path, "/") {
		errs.add(field, "must be relative")
	}
	if len(path) >= 2 && path[1] == ':' {
		errs.add(field, "must not use drive-letter paths")
	}
	if strings.Contains(path, "//") {
		errs.add(field, "must not contain empty path segments")
	}
	if !filePathPattern.MatchString(path) {
		errs.add(field, "contains unsupported characters")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." || segment == "" {
			errs.add(field, "must not contain current, parent, or empty segments")
			return
		}
	}
}

func validateBase64Content(errs *ValidationErrors, field, content string, maxBytes int) {
	if content == "" {
		errs.add(field, "must not be empty")
		return
	}
	if base64.StdEncoding.DecodedLen(len(content)) > maxBytes+2 {
		errs.add(field, fmt.Sprintf("decoded content must be at most %d bytes", maxBytes))
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		errs.add(field, "must be valid base64")
		return
	}
	if len(decoded) > maxBytes {
		errs.add(field, fmt.Sprintf("decoded content must be at most %d bytes", maxBytes))
	}
}

func validateLimits(errs *ValidationErrors, limits Limits, opts ValidationOptions) {
	validateLimit(errs, "limits.cpu_ms", limits.CPUms, opts.MinLimits.CPUms, opts.HardMaxLimits.CPUms)
	validateLimit(errs, "limits.wall_ms", limits.Wallms, opts.MinLimits.Wallms, opts.HardMaxLimits.Wallms)
	validateLimit(errs, "limits.memory_mb", limits.MemoryMB, opts.MinLimits.MemoryMB, opts.HardMaxLimits.MemoryMB)
	validateLimit(errs, "limits.pids", limits.PIDs, opts.MinLimits.PIDs, opts.HardMaxLimits.PIDs)
	validateLimit(errs, "limits.stdout_bytes", limits.StdoutBytes, opts.MinLimits.StdoutBytes, opts.HardMaxLimits.StdoutBytes)
	validateLimit(errs, "limits.stderr_bytes", limits.StderrBytes, opts.MinLimits.StderrBytes, opts.HardMaxLimits.StderrBytes)
	validateLimit(errs, "limits.workspace_bytes", limits.WorkspaceBytes, opts.MinLimits.WorkspaceBytes, opts.HardMaxLimits.WorkspaceBytes)

	if limits.Wallms < limits.CPUms {
		errs.add("limits.wall_ms", "must be greater than or equal to cpu_ms")
	}
}

func validateLimit(errs *ValidationErrors, field string, value, minValue, maxValue int) {
	if value < minValue {
		errs.add(field, fmt.Sprintf("must be at least %d", minValue))
	}
	if value > maxValue {
		errs.add(field, fmt.Sprintf("must be at most %d", maxValue))
	}
}
