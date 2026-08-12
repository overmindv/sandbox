package kafka

import (
	"context"
	"errors"
	"testing"

	"overmindv/sandbox/internal/contracts"
)

func TestProcessorMalformedMessagePublishesDLQAndCommits(t *testing.T) {
	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "sandbox.run.requests.v1",
				Partition: 1,
				Offset:    42,
				Key:       []byte("raw-key"),
				Value:     []byte(`{"schema_version":`),
				Headers:   map[string]string{HeaderTraceID: "trace-header"},
			},
		},
	}
	producer := &fakeProducer{}
	processor := testProcessor(t, consumer, producer, nil)

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}

	if len(consumer.committed) != 1 {
		t.Fatalf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Fatalf("expected one published message, got %d", len(producer.messages))
	}
	if producer.messages[0].Topic != processor.Topics.DeadLetter {
		t.Fatalf("expected deadletter topic, got %q", producer.messages[0].Topic)
	}

	deadLetter, err := DecodeRunDeadLetter(producer.messages[0].Value)
	if err != nil {
		t.Fatalf("decode dead letter: %v", err)
	}
	if deadLetter.Reason != "malformed_json" {
		t.Fatalf("expected malformed_json reason, got %q", deadLetter.Reason)
	}
	if deadLetter.OriginalKey == nil || *deadLetter.OriginalKey != "raw-key" {
		t.Fatalf("expected original key raw-key, got %#v", deadLetter.OriginalKey)
	}
}

func TestProcessorInvalidRequestPublishesResultDLQAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	req.Code.Files[0].Path = "../solution.py"
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "sandbox.run.requests.v1",
				Partition: 2,
				Offset:    99,
				Key:       []byte(req.AttemptID),
				Value:     payload,
				Headers:   map[string]string{HeaderTraceID: "trace-header"},
			},
		},
	}
	producer := &fakeProducer{}
	processor := testProcessor(t, consumer, producer, nil)

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}

	if len(consumer.committed) != 1 {
		t.Fatalf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 2 {
		t.Fatalf("expected result and DLQ messages, got %d", len(producer.messages))
	}

	result, err := DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if producer.messages[0].Topic != processor.Topics.Results {
		t.Fatalf("expected result topic, got %q", producer.messages[0].Topic)
	}
	if result.Status != contracts.StatusInvalidRequest {
		t.Fatalf("expected invalid_request status, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Retryable {
		t.Fatalf("expected non-retryable result error, got %#v", result.Error)
	}

	deadLetter, err := DecodeRunDeadLetter(producer.messages[1].Value)
	if err != nil {
		t.Fatalf("decode dead letter: %v", err)
	}
	if producer.messages[1].Topic != processor.Topics.DeadLetter {
		t.Fatalf("expected deadletter topic, got %q", producer.messages[1].Topic)
	}
	if deadLetter.Reason != "invalid_request" {
		t.Fatalf("expected invalid_request reason, got %q", deadLetter.Reason)
	}
	if deadLetter.OriginalPayloadB64 == nil {
		t.Fatal("expected original payload to be preserved")
	}
}

func TestProcessorValidRequestDelegatesAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "sandbox.run.requests.v1",
				Partition: 0,
				Offset:    1,
				Key:       []byte(req.AttemptID),
				Value:     payload,
				Headers:   map[string]string{HeaderTraceID: "trace-header"},
			},
		},
	}
	producer := &fakeProducer{}
	handled := false
	handler := RunRequestHandlerFunc(func(ctx context.Context, envelope Message, decoded contracts.RunRequest) error {
		handled = true
		if decoded.AttemptID != req.AttemptID {
			t.Fatalf("expected attempt_id %q, got %q", req.AttemptID, decoded.AttemptID)
		}
		event := contracts.RunEvent{
			SchemaVersion: contracts.RunEventSchemaVersion,
			SubmissionID:  decoded.SubmissionID,
			AttemptID:     decoded.AttemptID,
			EventType:     contracts.EventAccepted,
			Status:        contracts.StatusQueued,
			TraceID:       decoded.TraceID,
			WorkerID:      "worker-1",
			CreatedAt:     fixedKafkaTime(),
		}
		msg, err := MessageForEvent(testTopics().Events, event, "worker-1")
		if err != nil {
			return err
		}
		return producer.WriteMessages(ctx, msg)
	})
	processor := testProcessor(t, consumer, producer, handler)

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}
	if !handled {
		t.Fatal("expected handler to be called")
	}
	if len(consumer.committed) != 1 {
		t.Fatalf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Fatalf("expected one event message, got %d", len(producer.messages))
	}

	event, err := DecodeRunEvent(producer.messages[0].Value)
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.EventType != contracts.EventAccepted {
		t.Fatalf("expected accepted event, got %q", event.EventType)
	}
}

func testProcessor(t *testing.T, consumer Consumer, producer Producer, handler RunRequestHandler) *Processor {
	t.Helper()
	processor, err := NewProcessor(consumer, producer, testTopics(), "worker-1", handler, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	processor.Now = fixedKafkaTime
	return processor
}

func testTopics() TopicNames {
	return TopicNames{
		Requests:   "sandbox.run.requests.v1",
		Events:     "sandbox.run.events.v1",
		Results:    "sandbox.run.results.v1",
		DeadLetter: "sandbox.run.deadletter.v1",
	}
}

type fakeConsumer struct {
	messages  []Message
	committed []Message
	closed    bool
}

func (c *fakeConsumer) FetchMessage(context.Context) (Message, error) {
	if len(c.messages) == 0 {
		return Message{}, errors.New("no messages")
	}
	msg := c.messages[0]
	c.messages = c.messages[1:]
	return msg, nil
}

func (c *fakeConsumer) CommitMessages(_ context.Context, messages ...Message) error {
	c.committed = append(c.committed, messages...)
	return nil
}

func (c *fakeConsumer) Close() error {
	c.closed = true
	return nil
}

type fakeProducer struct {
	messages []Message
	closed   bool
}

func (p *fakeProducer) WriteMessages(_ context.Context, messages ...Message) error {
	p.messages = append(p.messages, messages...)
	return nil
}

func (p *fakeProducer) Close() error {
	p.closed = true
	return nil
}

var _ Consumer = (*fakeConsumer)(nil)
var _ Producer = (*fakeProducer)(nil)
