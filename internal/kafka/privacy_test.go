package kafka

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeadLetterNeverContainsPrivatePayload(t *testing.T) {
	msg := Message{Key: []byte("SECRET-KEY"), Value: []byte(`{"hidden":"SECRET-INPUT"}`), Headers: map[string]string{"authorization": "SECRET-TOKEN"}}
	dlq := NewDeadLetterFromMessage(msg, "invalid_request", errors.New("SECRET-ERROR"), false, 0, "worker", time.Now())
	payload, _ := json.Marshal(dlq)
	if dlq.OriginalPayloadB64 != nil || len(dlq.OriginalHeaders) > 0 || strings.Contains(string(payload), "SECRET") {
		t.Fatalf("DLQ leaked: %s", payload)
	}
}

func TestDeadLetterEnvelopeRedactsUntrustedIdentifiers(t *testing.T) {
	p := &Processor{Topics: TopicNames{DeadLetter: "dlq"}, WorkerID: "worker", Now: time.Now}
	letter := NewDeadLetterFromMessage(Message{Topic: "requests", Value: []byte("SECRET")}, "invalid_request", errors.New("SECRET"), false, 0, "worker", time.Now())
	letter.TraceID = "SECRET-TRACE"
	letter.SubmissionID = "SECRET-SUBMISSION"
	letter.AttemptID = "SECRET-ATTEMPT"
	msg, err := p.messageForDeadLetter(letter)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(msg)
	if strings.Contains(string(msg.Value), "SECRET") || strings.Contains(string(msg.Key), "SECRET") || strings.Contains(string(encoded), "SECRET") {
		t.Fatalf("DLQ envelope leaked: %s", encoded)
	}
}
