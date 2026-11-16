package kafka

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/security"
)

func TestProcessorMalformedMessagePublishesDLQAndCommits(t *testing.T) {
	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
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
		t.Errorf("process once: %v", err)
	}

	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one published message, got %d", len(producer.messages))
	}
	if producer.messages[0].Topic != processor.Topics.DeadLetter {
		t.Errorf("expected deadletter topic, got %q", producer.messages[0].Topic)
	}

	deadLetter, err := DecodeRunDeadLetter(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode dead letter: %v", err)
	}
	if deadLetter.Reason != "malformed_json" {
		t.Errorf("expected malformed_json reason, got %q", deadLetter.Reason)
	}
	if deadLetter.OriginalKey == nil || *deadLetter.OriginalKey != "raw-key" {
		t.Errorf("expected original key raw-key, got %#v", deadLetter.OriginalKey)
	}
}

func TestProcessorInvalidRequestPublishesResultDLQAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	req.Code.Files[0].Path = "../solution.py"
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
				Partition: 2,
				Offset:    99,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{HeaderTraceID: "trace-header"},
			},
		},
	}
	producer := &fakeProducer{}
	processor := testProcessor(t, consumer, producer, nil)

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Errorf("process once: %v", err)
	}

	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 2 {
		t.Errorf("expected result and DLQ messages, got %d", len(producer.messages))
	}

	result, err := DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode result: %v", err)
	}
	if producer.messages[0].Topic != processor.Topics.Results {
		t.Errorf("expected result topic, got %q", producer.messages[0].Topic)
	}
	if string(producer.messages[0].Key) != req.SubmissionID {
		t.Errorf("expected result key %q, got %q", req.SubmissionID, string(producer.messages[0].Key))
	}
	if result.Status != contracts.StatusInvalidRequest {
		t.Errorf("expected invalid_request status, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Retryable {
		t.Errorf("expected non-retryable result error, got %#v", result.Error)
	}

	deadLetter, err := DecodeRunDeadLetter(producer.messages[1].Value)
	if err != nil {
		t.Errorf("decode dead letter: %v", err)
	}
	if producer.messages[1].Topic != processor.Topics.DeadLetter {
		t.Errorf("expected deadletter topic, got %q", producer.messages[1].Topic)
	}
	if string(producer.messages[1].Key) != req.SubmissionID {
		t.Errorf("expected deadletter key %q, got %q", req.SubmissionID, string(producer.messages[1].Key))
	}
	if deadLetter.Reason != "invalid_request" {
		t.Errorf("expected invalid_request reason, got %q", deadLetter.Reason)
	}
	if deadLetter.OriginalPayloadB64 == nil {
		t.Error("expected original payload to be preserved")
	}
}

func TestProcessorValidRequestDelegatesAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
				Partition: 0,
				Offset:    1,
				Key:       []byte(req.SubmissionID),
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
			t.Errorf("expected attempt_id %q, got %q", req.AttemptID, decoded.AttemptID)
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
		t.Errorf("process once: %v", err)
	}
	if !handled {
		t.Error("expected handler to be called")
	}
	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one event message, got %d", len(producer.messages))
	}
	if string(producer.messages[0].Key) != req.SubmissionID {
		t.Errorf("expected event key %q, got %q", req.SubmissionID, string(producer.messages[0].Key))
	}

	event, err := DecodeRunEvent(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode event: %v", err)
	}
	if event.EventType != contracts.EventAccepted {
		t.Errorf("expected accepted event, got %q", event.EventType)
	}
}

func TestProcessorRetryableHandlerErrorSchedulesRetryAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
				Partition: 0,
				Offset:    10,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &fakeProducer{}
	processor := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		return NewRetryableProcessingError(ErrorClassInfra, "gojudge_unavailable", errors.New("dial tcp failed"), false)
	}))
	processor.RetryPolicy = RetryPolicy{
		Enabled:            true,
		MaxAttempts:        3,
		InitialBackoff:     time.Second,
		MaxBackoff:         10 * time.Second,
		BackoffMultiplier:  2,
		EarlyRetrySleepCap: time.Millisecond,
	}
	processor.Metrics = observability.NewSchedulerMetrics()

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Errorf("process once: %v", err)
		return
	}
	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one retry message, got %d", len(producer.messages))
		return
	}

	retryMessage := producer.messages[0]
	if retryMessage.Topic != processor.Topics.Retry {
		t.Errorf("expected retry topic, got %q", retryMessage.Topic)
	}
	if retryMessage.Headers[HeaderRetryCount] != "1" {
		t.Errorf("expected retry count 1, got %q", retryMessage.Headers[HeaderRetryCount])
	}
	if retryMessage.Headers[HeaderRetryReason] != "gojudge_unavailable" {
		t.Errorf("expected retry reason, got %q", retryMessage.Headers[HeaderRetryReason])
	}
	if retryMessage.Headers[HeaderOriginalTopic] != "code-execution.requests.v1" {
		t.Errorf("expected original topic header, got %q", retryMessage.Headers[HeaderOriginalTopic])
	}
	metrics := processor.Metrics.Snapshot()
	if metrics.RetriesScheduled["gojudge_unavailable"] != 1 {
		t.Errorf("expected retry metric, got %#v", metrics.RetriesScheduled)
	}
}

func TestProcessorRetryMessageTooEarlyIsNotCommitted(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.retry.v1",
				Partition: 0,
				Offset:    12,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers: map[string]string{
					HeaderTraceID:        req.TraceID,
					HeaderRetryCount:     "1",
					HeaderRetryReason:    "gojudge_unavailable",
					HeaderRetryNotBefore: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
				},
			},
		},
	}
	producer := &fakeProducer{}
	handlerCalled := false
	processor := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		handlerCalled = true
		return nil
	}))
	processor.RetryPolicy = RetryPolicy{
		Enabled:            true,
		MaxAttempts:        3,
		InitialBackoff:     time.Second,
		MaxBackoff:         10 * time.Second,
		BackoffMultiplier:  2,
		EarlyRetrySleepCap: time.Millisecond,
	}
	processor.Metrics = observability.NewSchedulerMetrics()

	err = processor.ProcessOnce(context.Background())
	if !errors.Is(err, ErrRetryNotReady) {
		t.Errorf("expected ErrRetryNotReady, got %v", err)
		return
	}
	if handlerCalled {
		t.Error("handler must not be called before retry not-before")
	}
	if len(consumer.committed) != 0 {
		t.Errorf("expected no committed messages, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 0 {
		t.Errorf("expected no published messages, got %d", len(producer.messages))
	}
	metrics := processor.Metrics.Snapshot()
	if metrics.RetriesDelayed["gojudge_unavailable"] != 1 {
		t.Errorf("expected delayed retry metric, got %#v", metrics.RetriesDelayed)
	}
}

func TestProcessorRetryExhaustedPublishesResultDLQAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.retry.v1",
				Partition: 1,
				Offset:    11,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers: map[string]string{
					HeaderTraceID:           req.TraceID,
					HeaderRetryCount:        "2",
					HeaderRetryReason:       "gojudge_unavailable",
					HeaderOriginalTopic:     "code-execution.requests.v1",
					HeaderOriginalPartition: "0",
					HeaderOriginalOffset:    "10",
				},
			},
		},
	}
	producer := &fakeProducer{}
	processor := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		return NewRetryableProcessingError(ErrorClassInfra, "gojudge_unavailable", errors.New("dial tcp failed"), false)
	}))
	processor.RetryPolicy = RetryPolicy{Enabled: true, MaxAttempts: 3, BackoffMultiplier: 2}
	processor.Metrics = observability.NewSchedulerMetrics()

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Errorf("process once: %v", err)
		return
	}
	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 2 {
		t.Errorf("expected result and DLQ messages, got %d", len(producer.messages))
		return
	}

	result, err := DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode exhausted result: %v", err)
		return
	}
	if result.Status != contracts.StatusInternalError {
		t.Errorf("expected internal_error result, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Retryable {
		t.Errorf("expected non-retryable exhausted error result, got %#v", result.Error)
	}

	deadLetter, err := DecodeRunDeadLetter(producer.messages[1].Value)
	if err != nil {
		t.Errorf("decode exhausted dead letter: %v", err)
		return
	}
	if deadLetter.OriginalTopic != "code-execution.requests.v1" {
		t.Errorf("expected original request topic, got %q", deadLetter.OriginalTopic)
	}
	if deadLetter.OriginalOffset != 10 {
		t.Errorf("expected original offset 10, got %d", deadLetter.OriginalOffset)
	}
	if deadLetter.OriginalHeaders[HeaderRetryCount] != "2" {
		t.Errorf("expected original retry headers, got %#v", deadLetter.OriginalHeaders)
	}
	metrics := processor.Metrics.Snapshot()
	if metrics.RetriesExhausted["gojudge_unavailable"] != 1 {
		t.Errorf("expected retry exhausted metric, got %#v", metrics.RetriesExhausted)
	}
	if metrics.DLQPublished["gojudge_unavailable"] != 1 {
		t.Errorf("expected DLQ metric, got %#v", metrics.DLQPublished)
	}
}

func TestProcessorNonRetryableUserErrorPublishesResultDLQAndCommits(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
				Partition: 0,
				Offset:    12,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{HeaderTraceID: req.TraceID},
			},
		},
	}
	producer := &fakeProducer{}
	processor := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		return NewProcessingError(ErrorClassUser, "attempt_conflict", errors.New("attempt id reused"), false)
	}))

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Errorf("process once: %v", err)
		return
	}
	if len(consumer.committed) != 1 {
		t.Errorf("expected one committed message, got %d", len(consumer.committed))
	}
	if len(producer.messages) != 2 {
		t.Errorf("expected result and DLQ messages, got %d", len(producer.messages))
		return
	}
	result, err := DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode user error result: %v", err)
		return
	}
	if result.Status != contracts.StatusInvalidRequest {
		t.Errorf("expected invalid_request result, got %q", result.Status)
	}
	if result.Error == nil || result.Error.Code != "attempt_conflict" {
		t.Errorf("expected attempt_conflict error, got %#v", result.Error)
	}
	deadLetter, err := DecodeRunDeadLetter(producer.messages[1].Value)
	if err != nil {
		t.Errorf("decode user error dead letter: %v", err)
		return
	}
	if deadLetter.Reason != "attempt_conflict" {
		t.Errorf("expected attempt_conflict DLQ, got %q", deadLetter.Reason)
	}
}

func TestProcessorOversizedPayloadPublishesDLQBeforeDecode(t *testing.T) {
	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
				Partition: 0,
				Offset:    2,
				Key:       []byte("raw-key"),
				Value:     []byte(`{"schema_version":"sandbox.run.request.v1"}`),
				Headers:   map[string]string{HeaderTraceID: "trace-header"},
			},
		},
	}
	producer := &fakeProducer{}
	handled := false
	processor := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		handled = true
		return nil
	}))
	policy := security.DefaultPolicy()
	policy.MaxPayloadBytes = 8
	processor.SecurityGuard = security.NewGuard(policy)

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Errorf("process once: %v", err)
	}
	if handled {
		t.Error("expected oversized payload not to reach handler")
	}
	if len(producer.messages) != 1 {
		t.Errorf("expected one DLQ message, got %d", len(producer.messages))
		return
	}
	deadLetter, err := DecodeRunDeadLetter(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode dead letter: %v", err)
		return
	}
	if deadLetter.Reason != "payload_rejected" {
		t.Errorf("expected payload_rejected, got %q", deadLetter.Reason)
	}
}

func TestProcessorSecurityRejectsArchiveBeforeHandler(t *testing.T) {
	req := validKafkaRunRequest()
	req.Code.Files[0].Path = "solution.zip"
	req.Code.Files[0].ContentB64 = encodeKafkaFixture("PK\x03\x04archive")
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Errorf("encode request: %v", err)
		return
	}

	consumer := &fakeConsumer{
		messages: []Message{
			{
				Topic:     "code-execution.requests.v1",
				Partition: 0,
				Offset:    3,
				Key:       []byte(req.SubmissionID),
				Value:     payload,
				Headers:   map[string]string{HeaderTraceID: "trace-header"},
			},
		},
	}
	producer := &fakeProducer{}
	handled := false
	processor := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
		handled = true
		return nil
	}))
	processor.Metrics = observability.NewSchedulerMetrics()
	var logs bytes.Buffer
	processor.Logger = observability.NewLogger("debug", "json", &logs)
	processor.Audit = observability.NewAuditLogger(true, processor.Logger)

	if err := processor.ProcessOnce(context.Background()); err != nil {
		t.Errorf("process once: %v", err)
	}
	if handled {
		t.Error("expected archive payload not to reach handler")
	}
	if len(producer.messages) != 2 {
		t.Errorf("expected result and DLQ messages, got %d", len(producer.messages))
		return
	}
	result, err := DecodeRunResult(producer.messages[0].Value)
	if err != nil {
		t.Errorf("decode result: %v", err)
		return
	}
	if result.Status != contracts.StatusInvalidRequest {
		t.Errorf("expected invalid_request, got %q", result.Status)
	}
	if !strings.Contains(logs.String(), "security_reject") {
		t.Errorf("expected security audit log, got %s", logs.String())
	}
	metrics := processor.Metrics.Snapshot()
	if metrics.Consumed[processor.Topics.Requests] != 1 {
		t.Errorf("expected consumed request metric, got %#v", metrics.Consumed)
	}
	if metrics.Produced[processor.Topics.Results] != 1 || metrics.Produced[processor.Topics.DeadLetter] != 1 {
		t.Errorf("expected result and DLQ produced metrics, got %#v", metrics.Produced)
	}
	if metrics.ResultStatuses[string(contracts.StatusInvalidRequest)] != 1 {
		t.Errorf("expected invalid_request result metric, got %#v", metrics.ResultStatuses)
	}
}

func testProcessor(t *testing.T, consumer Consumer, producer Producer, handler RunRequestHandler) *Processor {
	t.Helper()
	processor, err := NewProcessor(consumer, producer, testTopics(), "worker-1", handler, nil)
	if err != nil {
		t.Errorf("new processor: %v", err)
	}
	processor.Now = fixedKafkaTime
	return processor
}

func testTopics() TopicNames {
	return TopicNames{
		Requests:   "code-execution.requests.v1",
		Retry:      "code-execution.retry.v1",
		Events:     "code-execution.events.v1",
		Results:    "code-execution.results.v1",
		DeadLetter: "code-execution.deadletter.v1",
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

func TestProcessorRejectsWrongSubmissionKeyBeforeHandler(t *testing.T) {
	req := validKafkaRunRequest()
	payload, err := EncodeRunRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", req.AttemptID, "other"} {
		called := false
		consumer := &fakeConsumer{messages: []Message{{Topic: testTopics().Requests, Key: []byte(key), Value: payload}}}
		producer := &fakeProducer{}
		p := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error { called = true; return nil }))
		if err := p.ProcessOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatal("invalid key reached handler")
		}
		result, err := DecodeRunResult(producer.messages[0].Value)
		if err != nil || result.Status != contracts.StatusInvalidRequest {
			t.Fatalf("invalid-key result: %s %v", result.Status, err)
		}
	}
}

func TestProcessorInvalidVersionOmitsEvaluationContext(t *testing.T) {
	for _, version := range []string{"", "invalid"} {
		req := validKafkaRunRequest()
		req.Execution = &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"x"}}
		req.Metadata = map[string]any{"result_context_version": "1"}
		if version != "" {
			req.Metadata["task_version_id"] = version
		}
		payload, err := EncodeRunRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		consumer := &fakeConsumer{messages: []Message{{Topic: testTopics().Requests, Key: []byte(req.SubmissionID), Value: payload}}}
		producer := &fakeProducer{}
		p := testProcessor(t, consumer, producer, RunRequestHandlerFunc(func(context.Context, Message, contracts.RunRequest) error {
			t.Fatal("invalid version executed")
			return nil
		}))
		if err := p.ProcessOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		result, err := DecodeRunResult(producer.messages[0].Value)
		if err != nil || result.Status != contracts.StatusInvalidRequest || result.EvaluationContext != nil {
			t.Fatalf("invalid rejection: %+v %v", result, err)
		}
	}
}
