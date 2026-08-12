package kafka

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/contracts"
)

func TestRunRequestSerializationRoundTrip(t *testing.T) {
	req := validKafkaRunRequest()

	encoded, err := EncodeRunRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	decoded, err := DecodeRunRequest(encoded)
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}

	if decoded.AttemptID != req.AttemptID {
		t.Fatalf("expected attempt_id %q, got %q", req.AttemptID, decoded.AttemptID)
	}
	if !decoded.CreatedAt.Equal(req.CreatedAt) {
		t.Fatalf("expected created_at %s, got %s", req.CreatedAt, decoded.CreatedAt)
	}
}

func TestDecodeRunRequestRejectsUnknownFields(t *testing.T) {
	req := validKafkaRunRequest()
	encoded, err := EncodeRunRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	raw["unexpected"] = true
	encoded, err = json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal request with unknown field: %v", err)
	}

	_, err = DecodeRunRequest(encoded)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestEventResultAndDeadLetterSerialization(t *testing.T) {
	now := fixedKafkaTime()
	exitCode := 0
	event := contracts.RunEvent{
		SchemaVersion: contracts.RunEventSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		EventType:     contracts.EventStarted,
		Status:        contracts.StatusRunning,
		TraceID:       "trace-111",
		WorkerID:      "worker-1",
		CreatedAt:     now,
	}
	result := contracts.RunResult{
		SchemaVersion: contracts.RunResultSchemaVersion,
		SubmissionID:  event.SubmissionID,
		AttemptID:     event.AttemptID,
		Status:        contracts.StatusPassed,
		Summary:       contracts.ResultSummary{TestsTotal: 1, TestsPassed: 1},
		Timing:        contracts.ResultTiming{RunMS: 10, TotalMS: 10},
		Resources:     contracts.ResultResources{ExitCode: &exitCode},
		Logs:          contracts.ResultLogs{Stdout: "ok\n"},
		Error:         nil,
		TraceID:       event.TraceID,
		WorkerID:      event.WorkerID,
		CreatedAt:     now,
	}
	key := "bad-key"
	payload := "e30="
	deadLetter := contracts.RunDeadLetter{
		SchemaVersion:      contracts.RunDeadLetterSchemaVersion,
		SubmissionID:       event.SubmissionID,
		AttemptID:          event.AttemptID,
		Reason:             "invalid_request",
		Retryable:          false,
		OriginalTopic:      "sandbox.run.requests.v1",
		OriginalPartition:  1,
		OriginalOffset:     42,
		OriginalKey:        &key,
		OriginalPayloadB64: &payload,
		TraceID:            event.TraceID,
		WorkerID:           event.WorkerID,
		FailedAt:           now,
	}

	eventBytes, err := EncodeRunEvent(event)
	if err != nil {
		t.Fatalf("encode event: %v", err)
	}
	if _, err := DecodeRunEvent(eventBytes); err != nil {
		t.Fatalf("decode event: %v", err)
	}

	resultBytes, err := EncodeRunResult(result)
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	if _, err := DecodeRunResult(resultBytes); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	deadLetterBytes, err := EncodeRunDeadLetter(deadLetter)
	if err != nil {
		t.Fatalf("encode dead letter: %v", err)
	}
	if _, err := DecodeRunDeadLetter(deadLetterBytes); err != nil {
		t.Fatalf("decode dead letter: %v", err)
	}
}

func validKafkaRunRequest() contracts.RunRequest {
	return contracts.RunRequest{
		SchemaVersion: contracts.RunRequestSchemaVersion,
		SubmissionID:  "11111111-1111-4111-8111-111111111111",
		AttemptID:     "22222222-2222-4222-8222-222222222222",
		UserID:        "33333333-3333-4333-8333-333333333333",
		TaskID:        "44444444-4444-4444-8444-444444444444",
		Language: contracts.LanguageSpec{
			ID:      "python",
			Version: "3.12",
		},
		Code: contracts.CodeSpec{
			Entrypoint: "solution.py",
			Files: []contracts.SourceFile{
				{
					Path:       "solution.py",
					ContentB64: encodeKafkaFixture("def add(a, b):\n    return a + b\n"),
				},
			},
		},
		Tests: contracts.TestSpec{
			Mode:       contracts.TestModePytest,
			Source:     contracts.TestSourceInline,
			Entrypoint: "test_solution.py",
			Files: []contracts.SourceFile{
				{
					Path:       "test_solution.py",
					ContentB64: encodeKafkaFixture("from solution import add\n\ndef test_add():\n    assert add(1, 2) == 3\n"),
				},
			},
		},
		Limits: contracts.Limits{
			CPUms:          2000,
			Wallms:         5000,
			MemoryMB:       128,
			PIDs:           32,
			StdoutBytes:    32768,
			StderrBytes:    32768,
			WorkspaceBytes: 1048576,
		},
		TraceID:   "trace-111",
		CreatedAt: fixedKafkaTime(),
	}
}

func encodeKafkaFixture(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func fixedKafkaTime() time.Time {
	return time.Date(2026, 8, 12, 19, 0, 0, 0, time.UTC)
}
