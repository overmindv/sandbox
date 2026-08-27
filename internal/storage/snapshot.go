package storage

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"overmindv/sandbox/internal/contracts"
)

const (
	aggregateTypeRun     = "run"
	aggregateTypeAttempt = "attempt"
	defaultLimitsProfile = "request"
)

var storageUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type RunSnapshot struct {
	RunID       string
	RequestHash string
	Run         RunRecord
	Files       []RunFileRecord
	Tests       []RunTestRecord
}

func BuildRunSnapshot(req contracts.RunRequest, createdAt time.Time) (RunSnapshot, error) {
	requestHash, err := RequestHash(req)
	if err != nil {
		return RunSnapshot{}, err
	}

	runID := StableUUID("run", req.SubmissionID, requestHash)
	snapshot := RunSnapshot{
		RunID:       runID,
		RequestHash: requestHash,
		Run: RunRecord{
			RunID:           runID,
			SubmissionID:    req.SubmissionID,
			UserID:          req.UserID,
			TaskID:          req.TaskID,
			TaskVersionID:   metadataUUID(req.Metadata, "task_version_id"),
			LanguageID:      req.Language.ID,
			LanguageVersion: req.Language.Version,
			Status:          RunStatusRunning,
			RequestHash:     requestHash,
			LimitsProfileID: metadataStringDefault(req.Metadata, "limits_profile_id", defaultLimitsProfile),
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt,
		},
	}

	files, err := buildRunFiles(runID, req, createdAt)
	if err != nil {
		return RunSnapshot{}, err
	}
	tests, err := buildRunTests(runID, req, createdAt)
	if err != nil {
		return RunSnapshot{}, err
	}
	snapshot.Files = files
	snapshot.Tests = tests
	return snapshot, nil
}

func RequestHash(req contracts.RunRequest) (string, error) {
	normalized := req
	normalized.AttemptID = ""
	normalized.TraceID = ""
	normalized.CreatedAt = time.Time{}
	normalized.Metadata = normalizeMetadata(req.Metadata)

	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func StableUUID(parts ...string) string {
	joined := strings.Join(parts, "\x00")
	sum := sha1.Sum([]byte(joined))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func ResultOutboxEventID(attemptID string) string {
	return StableUUID("outbox", "result", attemptID)
}

func buildRunFiles(runID string, req contracts.RunRequest, createdAt time.Time) ([]RunFileRecord, error) {
	records := make([]RunFileRecord, 0, len(req.Code.Files)+len(req.Tests.Files))
	for _, file := range req.Code.Files {
		record, err := runFileRecord(runID, RunFileKindSource, file, createdAt)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	for _, file := range req.Tests.Files {
		record, err := runFileRecord(runID, RunFileKindTest, file, createdAt)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func runFileRecord(runID string, kind RunFileKind, file contracts.SourceFile, createdAt time.Time) (RunFileRecord, error) {
	content, err := base64.StdEncoding.DecodeString(file.ContentB64)
	if err != nil {
		return RunFileRecord{}, err
	}

	contentHash := file.ChecksumSHA256
	if contentHash == "" {
		sum := sha256.Sum256(content)
		contentHash = hex.EncodeToString(sum[:])
	}

	return RunFileRecord{
		FileID:      StableUUID("run_file", runID, string(kind), file.Path),
		RunID:       runID,
		Kind:        kind,
		Path:        file.Path,
		ContentHash: contentHash,
		SizeBytes:   int64(len(content)),
		CreatedAt:   createdAt,
	}, nil
}

func buildRunTests(runID string, req contracts.RunRequest, createdAt time.Time) ([]RunTestRecord, error) {
	switch req.Tests.Source {
	case contracts.TestSourceInline:
		records := make([]RunTestRecord, 0, len(req.Tests.Files))
		for i, file := range req.Tests.Files {
			payload, err := json.Marshal(file)
			if err != nil {
				return nil, err
			}
			records = append(records, RunTestRecord{
				RunTestID:  StableUUID("run_test", runID, "inline", file.Path),
				RunID:      runID,
				Name:       file.Path,
				Source:     string(req.Tests.Source),
				Mode:       string(req.Tests.Mode),
				OrderIndex: i,
				TestJSON:   json.RawMessage(payload),
				CreatedAt:  createdAt,
			})
		}
		return records, nil
	case contracts.TestSourceObjectStorage:
		payload, err := json.Marshal(req.Tests)
		if err != nil {
			return nil, err
		}
		return []RunTestRecord{
			{
				RunTestID:  StableUUID("run_test", runID, "object_storage", req.Tests.SuiteID, req.Tests.ChecksumSHA256),
				RunID:      runID,
				TestRefID:  req.Tests.SuiteID,
				Name:       req.Tests.SuiteID,
				Source:     string(req.Tests.Source),
				Mode:       string(req.Tests.Mode),
				OrderIndex: 0,
				TestJSON:   json.RawMessage(payload),
				CreatedAt:  createdAt,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported test source %q", req.Tests.Source)
	}
}

func AttemptTestResultsFromRunResult(result contracts.RunResult, createdAt time.Time) []AttemptTestResultRecord {
	records := make([]AttemptTestResultRecord, 0, len(result.Tests))
	for i, test := range result.Tests {
		records = append(records, AttemptTestResultRecord{
			AttemptTestResultID: StableUUID("attempt_test_result", result.AttemptID, fmt.Sprintf("%d", i), test.Name),
			AttemptID:           result.AttemptID,
			OrderIndex:          i,
			Name:                test.Name,
			Status:              test.Status,
			DurationMS:          test.DurationMS,
			Message:             test.Message,
			CreatedAt:           createdAt,
		})
	}
	return records
}

func ArtifactsFromRunResult(runID string, result contracts.RunResult, createdAt time.Time) []ArtifactRecord {
	records := make([]ArtifactRecord, 0, 2)
	if result.Logs.StdoutArtifact != nil {
		records = append(records, artifactFromReference(runID, result.AttemptID, ArtifactKindStdout, *result.Logs.StdoutArtifact, createdAt))
	} else if result.Logs.Stdout != "" {
		records = append(records, logArtifact(runID, result.AttemptID, ArtifactKindStdout, result.Logs.Stdout, result.Logs.Truncated, createdAt))
	}
	if result.Logs.StderrArtifact != nil {
		records = append(records, artifactFromReference(runID, result.AttemptID, ArtifactKindStderr, *result.Logs.StderrArtifact, createdAt))
	} else if result.Logs.Stderr != "" {
		records = append(records, logArtifact(runID, result.AttemptID, ArtifactKindStderr, result.Logs.Stderr, result.Logs.Truncated, createdAt))
	}
	return records
}

func ResultOutboxEvent(runID string, result contracts.RunResult, topic string, createdAt time.Time) (OutboxEventRecord, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return OutboxEventRecord{}, err
	}
	headers, err := json.Marshal(map[string]string{
		"schema_version": result.SchemaVersion,
		"trace_id":       result.TraceID,
		"producer":       result.WorkerID,
	})
	if err != nil {
		return OutboxEventRecord{}, err
	}
	return OutboxEventRecord{
		EventID:       ResultOutboxEventID(result.AttemptID),
		AggregateType: aggregateTypeAttempt,
		AggregateID:   result.AttemptID,
		RunID:         runID,
		AttemptID:     result.AttemptID,
		EventType:     OutboxEventRunResultReady,
		Topic:         topic,
		Key:           result.SubmissionID,
		PayloadJSON:   json.RawMessage(payload),
		HeadersJSON:   json.RawMessage(headers),
		Status:        OutboxStatusPending,
		AvailableAt:   createdAt,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}, nil
}

func EmptyHeadersJSON() json.RawMessage {
	return json.RawMessage(`{}`)
}

func logArtifact(runID string, attemptID string, kind ArtifactKind, content string, truncated bool, createdAt time.Time) ArtifactRecord {
	sum := sha256.Sum256([]byte(content))
	return ArtifactRecord{
		ArtifactID:      StableUUID("artifact", attemptID, string(kind)),
		RunID:           runID,
		AttemptID:       attemptID,
		Kind:            kind,
		StorageProvider: ArtifactStoragePostgresJSON,
		ContentType:     "text/plain; charset=utf-8",
		ContentHash:     hex.EncodeToString(sum[:]),
		SizeBytes:       int64(len([]byte(content))),
		Truncated:       truncated,
		CreatedAt:       createdAt,
	}
}

func artifactFromReference(runID string, attemptID string, kind ArtifactKind, ref contracts.ArtifactReference, createdAt time.Time) ArtifactRecord {
	artifactID := ref.ID
	if artifactID == "" {
		artifactID = StableUUID("artifact", attemptID, string(kind))
	}
	storageProvider := ArtifactStorageProvider(ref.Provider)
	if storageProvider == "" {
		storageProvider = ArtifactStorageS3
	}
	contentType := ref.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return ArtifactRecord{
		ArtifactID:      artifactID,
		RunID:           runID,
		AttemptID:       attemptID,
		Kind:            kind,
		StorageProvider: storageProvider,
		Bucket:          ref.Bucket,
		ObjectKey:       ref.Key,
		ContentType:     contentType,
		ContentHash:     ref.SHA256,
		SizeBytes:       ref.SizeBytes,
		Truncated:       ref.Truncated,
		CreatedAt:       createdAt,
	}
}

func normalizeMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	normalized := make(map[string]any, len(metadata))
	for _, key := range keys {
		normalized[key] = metadata[key]
	}
	return normalized
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func metadataStringDefault(metadata map[string]any, key string, fallback string) string {
	if value := metadataString(metadata, key); value != "" {
		return value
	}
	return fallback
}

func metadataUUID(metadata map[string]any, key string) string {
	value := metadataString(metadata, key)
	if storageUUIDPattern.MatchString(value) {
		return value
	}
	return ""
}
