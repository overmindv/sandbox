package kafka

import (
	"context"
	"errors"
	"overmindv/sandbox/internal/contracts"
	"testing"
	"time"
)

func TestExpiredAttemptNeverExecutes(t *testing.T) {
	req := validKafkaRunRequest()
	req.CreatedAt = fixedKafkaTime().Add(-time.Minute)
	req.Metadata = map[string]any{"attempt_deadline": fixedKafkaTime().Add(-time.Second).Format(time.RFC3339Nano)}
	payload, _ := EncodeRunRequest(req)
	producer := &fakeProducer{}
	called := false
	p := testProcessor(t, &fakeConsumer{}, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error { called = true; return nil }))
	if err := p.ProcessMessage(context.Background(), Message{Topic: testTopics().Requests, Key: []byte(req.SubmissionID), Value: payload}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("expired attempt executed")
	}
	found := false
	for _, m := range producer.messages {
		if m.Topic == testTopics().Results {
			r, err := DecodeRunResult(m.Value)
			if err != nil || r.Status != contracts.StatusInternalError || r.Error.Code != "attempt_deadline_exceeded" {
				t.Fatalf("terminal result %+v %v", r, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no terminal result")
	}
}

func TestRetryCannotExtendAttemptDeadline(t *testing.T) {
	req := validKafkaRunRequest()
	req.Metadata = map[string]any{"attempt_deadline": fixedKafkaTime().Add(time.Second).Format(time.RFC3339Nano)}
	payload, _ := EncodeRunRequest(req)
	producer := &fakeProducer{}
	p := testProcessor(t, &fakeConsumer{}, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		return NewRetryableProcessingError(ErrorClassInfra, "offline", errors.New("offline"), false)
	}))
	p.RetryPolicy = RetryPolicy{Enabled: true, MaxAttempts: 5, InitialBackoff: 2 * time.Second}
	if err := p.ProcessMessage(context.Background(), Message{Topic: testTopics().Requests, Key: []byte(req.SubmissionID), Value: payload}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range producer.messages {
		if m.Topic == testTopics().Retry {
			t.Fatal("retry beyond deadline")
		}
		if m.Topic == testTopics().Results {
			found = true
		}
	}
	if !found {
		t.Fatal("no terminal result")
	}
}

func TestRetryBudgetIsCappedAndOverflowSafe(t *testing.T) {
	p := (RetryPolicy{MaxAttempts: 100, InitialBackoff: time.Hour, MaxBackoff: 2 * time.Hour, BackoffMultiplier: 100}).Normalize()
	if p.MaxAttempts != 5 || p.NextBackoff(10000) != 30*time.Second {
		t.Fatalf("unbounded policy: %+v", p)
	}
}
