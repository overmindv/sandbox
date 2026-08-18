package kafka

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/security"
	"overmindv/sandbox/internal/storage"
)

type RunRequestHandler interface {
	HandleRunRequest(ctx context.Context, envelope Message, req contracts.RunRequest) error
}

type RunRequestHandlerFunc func(ctx context.Context, envelope Message, req contracts.RunRequest) error

func (f RunRequestHandlerFunc) HandleRunRequest(ctx context.Context, envelope Message, req contracts.RunRequest) error {
	return f(ctx, envelope, req)
}

type CommitPolicy struct {
	CommitAfterResultPublish bool
}

type RequestSecurityGuard interface {
	ValidatePayload(payload []byte) error
	ValidateRunRequest(req contracts.RunRequest) error
}

type Processor struct {
	Consumer          Consumer
	Producer          Producer
	Topics            TopicNames
	WorkerID          string
	CommitPolicy      CommitPolicy
	ValidationOptions contracts.ValidationOptions
	SecurityGuard     RequestSecurityGuard
	Handler           RunRequestHandler
	RetryPolicy       RetryPolicy
	RunStore          storage.RunStore
	Metrics           *observability.SchedulerMetrics
	Now               func() time.Time
	Logger            *observability.Logger
}

func NewProcessor(consumer Consumer, producer Producer, topics TopicNames, workerID string, handler RunRequestHandler, logger *observability.Logger) (*Processor, error) {
	if err := topics.Validate(); err != nil {
		return nil, err
	}
	if consumer == nil {
		return nil, errors.New("consumer is required")
	}
	if producer == nil {
		return nil, errors.New("producer is required")
	}
	if logger == nil {
		logger = observability.NewLogger("info", "json", nil)
	}

	return &Processor{
		Consumer: consumer,
		Producer: producer,
		Topics:   topics,
		WorkerID: workerID,
		CommitPolicy: CommitPolicy{
			CommitAfterResultPublish: true,
		},
		ValidationOptions: contracts.DefaultValidationOptions(),
		SecurityGuard:     security.NewGuard(security.DefaultPolicy()),
		RetryPolicy: RetryPolicy{
			Enabled:            true,
			MaxAttempts:        3,
			InitialBackoff:     500 * time.Millisecond,
			MaxBackoff:         30 * time.Second,
			BackoffMultiplier:  2,
			EarlyRetrySleepCap: 5 * time.Second,
		},
		Handler: handler,
		Now:     func() time.Time { return time.Now().UTC() },
		Logger:  logger,
	}, nil
}

func (p *Processor) ProcessOnce(ctx context.Context) error {
	msg, err := p.Consumer.FetchMessage(ctx)
	if err != nil {
		return err
	}

	if !p.CommitPolicy.CommitAfterResultPublish {
		if err := p.Consumer.CommitMessages(ctx, msg); err != nil {
			return err
		}
	}

	if err := p.ProcessMessage(ctx, msg); err != nil {
		if !p.CommitPolicy.CommitAfterResultPublish {
			return fmt.Errorf("%w: %w", ErrCommitBeforeHandling, err)
		}
		return err
	}

	if p.CommitPolicy.CommitAfterResultPublish {
		return p.Consumer.CommitMessages(ctx, msg)
	}
	return nil
}

func (p *Processor) Close() error {
	if p == nil {
		return nil
	}
	return errors.Join(p.Consumer.Close(), p.Producer.Close())
}

func (p *Processor) ProcessMessage(ctx context.Context, msg Message) error {
	traceID := msg.Headers[HeaderTraceID]
	ctx = observability.WithTraceID(ctx, traceID)

	if err := p.waitForRetryWindow(ctx, msg); err != nil {
		return err
	}

	if p.SecurityGuard != nil {
		if err := p.SecurityGuard.ValidatePayload(msg.Value); err != nil {
			p.Logger.Warn(ctx, "kafka payload rejected by security guard", "topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset, "error", err)
			return p.publishDeadLetter(ctx, msg, "payload_rejected", err, false, contracts.RunRequest{})
		}
	}

	req, err := DecodeRunRequest(msg.Value)
	if err != nil {
		if !json.Valid(msg.Value) {
			p.Logger.Warn(ctx, "malformed kafka message", "topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset, "error", err)
			return p.publishDeadLetter(ctx, msg, "malformed_json", err, false, contracts.RunRequest{})
		}

		var partial contracts.RunRequest
		_ = json.Unmarshal(msg.Value, &partial)
		if partial.TraceID != "" {
			ctx = observability.WithTraceID(ctx, partial.TraceID)
		}
		p.Logger.Warn(ctx, "invalid kafka request shape", "topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset, "error", err)
		return p.publishInvalidRequest(ctx, msg, partial, err)
	}

	if req.TraceID != "" {
		ctx = observability.WithTraceID(ctx, req.TraceID)
	}

	if err := contracts.ValidateRunRequestWithOptions(req, p.ValidationOptions); err != nil {
		p.Logger.Warn(ctx, "invalid run request", "attempt_id", req.AttemptID, "error", err)
		return p.publishInvalidRequest(ctx, msg, req, err)
	}
	if p.SecurityGuard != nil {
		if err := p.SecurityGuard.ValidateRunRequest(req); err != nil {
			p.Logger.Warn(ctx, "run request rejected by security guard", "attempt_id", req.AttemptID, "error", err)
			return p.publishInvalidRequest(ctx, msg, req, err)
		}
	}

	if p.Handler == nil {
		return p.handleProcessingError(ctx, msg, req, ErrNoRunRequestHandler)
	}
	if err := p.Handler.HandleRunRequest(ctx, msg, req); err != nil {
		return p.handleProcessingError(ctx, msg, req, err)
	}
	return nil
}

func (p *Processor) handleProcessingError(ctx context.Context, msg Message, req contracts.RunRequest, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	decision := ClassifyProcessingError(cause)
	if !decision.Retryable {
		return p.publishTerminalError(ctx, msg, req, decision)
	}

	policy := p.RetryPolicy.Normalize()
	retryCount := RetryCount(msg.Headers)
	if !policy.Enabled || policy.Exhausted(retryCount) {
		p.Logger.Error(ctx, "retry attempts exhausted", "attempt_id", req.AttemptID, "reason", decision.Reason, "retry_count", retryCount)
		if p.Metrics != nil {
			p.Metrics.IncRetryExhausted(decision.Reason)
		}
		return p.publishRetryExhausted(ctx, msg, req, decision, retryCount)
	}

	if err := p.markAttemptRetryableFailure(ctx, req, decision, retryCount); err != nil {
		return err
	}

	nextRetryCount := retryCount + 1
	backoff := policy.NextBackoff(retryCount)
	retryAt := p.now().Add(backoff)
	retryMessage := p.messageForRetry(msg, decision, nextRetryCount, retryAt)
	if err := p.appendOutboxForMessage(ctx, req, storage.OutboxEventRetryReady, retryMessage, nextRetryCount); err != nil {
		return err
	}
	if err := p.Producer.WriteMessages(ctx, retryMessage); err != nil {
		return err
	}

	p.Logger.Warn(ctx, "scheduled kafka retry", "attempt_id", req.AttemptID, "reason", decision.Reason, "retry_count", nextRetryCount, "retry_at", retryAt.Format(time.RFC3339Nano))
	if p.Metrics != nil {
		p.Metrics.IncRetryScheduled(decision.Reason)
	}
	return nil
}

func (p *Processor) publishInvalidRequest(ctx context.Context, msg Message, req contracts.RunRequest, cause error) error {
	result := contracts.RunResult{
		SchemaVersion: contracts.RunResultSchemaVersion,
		SubmissionID:  req.SubmissionID,
		AttemptID:     req.AttemptID,
		UserID:        req.UserID,
		TaskID:        req.TaskID,
		Language:      req.Language,
		Status:        contracts.StatusInvalidRequest,
		Summary:       contracts.ResultSummary{},
		Timing:        contracts.ResultTiming{},
		Resources:     contracts.ResultResources{},
		Logs:          contracts.ResultLogs{},
		Error: &contracts.RunError{
			Code:      "invalid_request",
			Message:   cause.Error(),
			Retryable: false,
		},
		TraceID:   traceIDFor(req, msg),
		WorkerID:  p.WorkerID,
		CreatedAt: p.now(),
	}

	resultMessage, err := p.messageForResult(result)
	if err != nil {
		return err
	}

	deadLetter := NewDeadLetterFromMessage(msg, "invalid_request", cause, false, 0, p.WorkerID, p.now())
	deadLetter.SubmissionID = req.SubmissionID
	deadLetter.AttemptID = req.AttemptID
	deadLetter.TraceID = traceIDFor(req, msg)
	deadLetterMessage, err := p.messageForDeadLetter(deadLetter)
	if err != nil {
		return err
	}

	if p.Metrics != nil {
		p.Metrics.IncErrorResult("invalid_request")
		p.Metrics.IncDLQPublished("invalid_request")
	}
	return p.Producer.WriteMessages(ctx, resultMessage, deadLetterMessage)
}

func (p *Processor) publishDeadLetter(ctx context.Context, msg Message, reason string, cause error, retryable bool, req contracts.RunRequest) error {
	deadLetter := NewDeadLetterFromMessage(msg, reason, cause, retryable, 0, p.WorkerID, p.now())
	deadLetter.SubmissionID = req.SubmissionID
	deadLetter.AttemptID = req.AttemptID
	deadLetter.TraceID = traceIDFor(req, msg)

	deadLetterMessage, err := p.messageForDeadLetter(deadLetter)
	if err != nil {
		return err
	}
	if err := p.appendOutboxForMessage(ctx, req, storage.OutboxEventDeadLetterReady, deadLetterMessage, RetryCount(msg.Headers)); err != nil {
		return err
	}
	if p.Metrics != nil {
		p.Metrics.IncDLQPublished(reason)
	}
	return p.Producer.WriteMessages(ctx, deadLetterMessage)
}

func (p *Processor) publishTerminalError(ctx context.Context, msg Message, req contracts.RunRequest, decision RetryDecision) error {
	if decision.Class == ErrorClassValidation || decision.Class == ErrorClassUser {
		result := p.errorResult(req, statusForErrorClass(decision.Class), decision, false)
		resultMessage, err := p.messageForResult(result)
		if err != nil {
			return err
		}
		if p.RunStore != nil && req.AttemptID != "" {
			if err := p.completeAttemptIfReserved(ctx, result); err != nil {
				return err
			}
		}
		if err := p.appendResultOutbox(ctx, result); err != nil {
			return err
		}

		deadLetter := NewDeadLetterFromMessage(msg, decision.Reason, errors.New(decision.Message), false, RetryCount(msg.Headers), p.WorkerID, p.now())
		deadLetter.SubmissionID = req.SubmissionID
		deadLetter.AttemptID = req.AttemptID
		deadLetter.TraceID = traceIDFor(req, msg)
		deadLetterMessage, err := p.messageForDeadLetter(deadLetter)
		if err != nil {
			return err
		}
		if err := p.appendOutboxForMessage(ctx, req, storage.OutboxEventDeadLetterReady, deadLetterMessage, RetryCount(msg.Headers)); err != nil {
			return err
		}
		if p.Metrics != nil {
			p.Metrics.IncErrorResult(decision.Reason)
			p.Metrics.IncDLQPublished(decision.Reason)
		}
		return p.Producer.WriteMessages(ctx, resultMessage, deadLetterMessage)
	}

	return p.publishDeadLetter(ctx, msg, decision.Reason, errors.New(decision.Message), false, req)
}

func (p *Processor) publishRetryExhausted(ctx context.Context, msg Message, req contracts.RunRequest, decision RetryDecision, retryCount int) error {
	messages := make([]Message, 0, 2)
	if known, ok, err := p.knownAttemptResult(ctx, req.AttemptID); err != nil {
		return err
	} else if ok {
		resultMessage, err := p.messageForResult(known)
		if err != nil {
			return err
		}
		messages = append(messages, resultMessage)
	} else if req.AttemptID != "" && decision.Reason != "attempt_in_progress" {
		result := p.errorResult(req, contracts.StatusInternalError, decision, false)
		if err := p.completeAttemptIfReserved(ctx, result); err != nil {
			return err
		}
		if err := p.appendResultOutbox(ctx, result); err != nil {
			return err
		}
		resultMessage, err := p.messageForResult(result)
		if err != nil {
			return err
		}
		messages = append(messages, resultMessage)
		if p.Metrics != nil {
			p.Metrics.IncErrorResult(decision.Reason)
		}
	}

	deadLetter := NewDeadLetterFromMessage(msg, decision.Reason, errors.New(decision.Message), true, retryCount, p.WorkerID, p.now())
	deadLetter.SubmissionID = req.SubmissionID
	deadLetter.AttemptID = req.AttemptID
	deadLetter.TraceID = traceIDFor(req, msg)
	deadLetterMessage, err := p.messageForDeadLetter(deadLetter)
	if err != nil {
		return err
	}
	if err := p.appendOutboxForMessage(ctx, req, storage.OutboxEventDeadLetterReady, deadLetterMessage, retryCount); err != nil {
		return err
	}
	messages = append(messages, deadLetterMessage)

	if p.Metrics != nil {
		p.Metrics.IncDLQPublished(decision.Reason)
	}
	return p.Producer.WriteMessages(ctx, messages...)
}

func (p *Processor) markAttemptRetryableFailure(ctx context.Context, req contracts.RunRequest, decision RetryDecision, retryCount int) error {
	if p.RunStore == nil || req.AttemptID == "" || !decision.ReleaseAttempt {
		return nil
	}
	if decision.Reason == "attempt_in_progress" {
		return nil
	}
	return p.RunStore.FailAttempt(ctx, storage.AttemptFailure{
		AttemptID:        req.AttemptID,
		SubmissionID:     req.SubmissionID,
		WorkerInstanceID: p.WorkerID,
		Status:           storage.AttemptStatusFailedInternal,
		ErrorClass:       string(decision.Class),
		Reason:           decision.Reason,
		Message:          decision.Message,
		DeliveryAttempts: p.RetryPolicy.Normalize().HandlingAttempt(retryCount),
		FailedAt:         p.now(),
	})
}

func (p *Processor) completeAttemptIfReserved(ctx context.Context, result contracts.RunResult) error {
	if p.RunStore == nil || result.AttemptID == "" {
		return nil
	}
	if _, ok, err := p.RunStore.GetAttempt(ctx, result.AttemptID); err != nil {
		return err
	} else if !ok {
		return nil
	}
	if known, ok, err := p.RunStore.GetAttemptResult(ctx, result.AttemptID); err != nil {
		return err
	} else if ok {
		_ = known
		return nil
	}
	return p.RunStore.CompleteAttempt(ctx, result)
}

func (p *Processor) knownAttemptResult(ctx context.Context, attemptID string) (contracts.RunResult, bool, error) {
	if p.RunStore == nil || attemptID == "" {
		return contracts.RunResult{}, false, nil
	}
	return p.RunStore.GetAttemptResult(ctx, attemptID)
}

func (p *Processor) messageForRetry(msg Message, decision RetryDecision, nextRetryCount int, retryAt time.Time) Message {
	headers := cloneHeaders(msg.Headers)
	headers[HeaderRetryCount] = strconv.Itoa(nextRetryCount)
	headers[HeaderRetryReason] = decision.Reason
	headers[HeaderRetryClass] = string(decision.Class)
	headers[HeaderRetryNotBefore] = retryAt.UTC().Format(time.RFC3339Nano)
	if headers[HeaderOriginalTopic] == "" {
		headers[HeaderOriginalTopic] = msg.Topic
		headers[HeaderOriginalPartition] = strconv.Itoa(msg.Partition)
		headers[HeaderOriginalOffset] = strconv.FormatInt(msg.Offset, 10)
	}

	return Message{
		Topic:   p.Topics.Retry,
		Key:     cloneBytes(msg.Key),
		Value:   cloneBytes(msg.Value),
		Headers: headers,
		Time:    p.now(),
	}
}

func (p *Processor) appendOutboxForMessage(ctx context.Context, req contracts.RunRequest, eventType storage.OutboxEventType, msg Message, retryCount int) error {
	if p.RunStore == nil || req.AttemptID == "" {
		return nil
	}
	attempt, ok, err := p.RunStore.GetAttempt(ctx, req.AttemptID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	payloadJSON := json.RawMessage(cloneBytes(msg.Value))
	if !json.Valid(payloadJSON) {
		encoded, err := json.Marshal(map[string]string{"payload_b64": base64.StdEncoding.EncodeToString(payloadJSON)})
		if err != nil {
			return err
		}
		payloadJSON = encoded
	}
	headersJSON, err := json.Marshal(msg.Headers)
	if err != nil {
		return err
	}

	now := p.now()
	eventID := storage.StableUUID("outbox", string(eventType), req.AttemptID, strconv.Itoa(retryCount), msg.Topic, strconv.FormatInt(msg.Offset, 10))
	event := storage.OutboxEventRecord{
		EventID:       eventID,
		AggregateType: "attempt",
		AggregateID:   req.AttemptID,
		RunID:         attempt.RunID,
		AttemptID:     req.AttemptID,
		EventType:     eventType,
		Topic:         msg.Topic,
		Key:           string(msg.Key),
		PayloadJSON:   payloadJSON,
		HeadersJSON:   headersJSON,
		Status:        storage.OutboxStatusPending,
		AvailableAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_, err = p.RunStore.AppendOutboxEvent(ctx, event)
	return err
}

func (p *Processor) appendResultOutbox(ctx context.Context, result contracts.RunResult) error {
	if p.RunStore == nil || result.AttemptID == "" {
		return nil
	}
	attempt, ok, err := p.RunStore.GetAttempt(ctx, result.AttemptID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	event, err := storage.ResultOutboxEvent(attempt.RunID, result, p.Topics.Results, p.now())
	if err != nil {
		return err
	}
	_, err = p.RunStore.AppendOutboxEvent(ctx, event)
	return err
}

func (p *Processor) waitForRetryWindow(ctx context.Context, msg Message) error {
	if msg.Topic != p.Topics.Retry {
		return nil
	}
	notBefore, ok := RetryNotBefore(msg.Headers)
	if !ok {
		return nil
	}
	now := p.now()
	if !notBefore.After(now) {
		return nil
	}

	sleepFor := notBefore.Sub(now)
	notReadyAfterSleep := false
	policy := p.RetryPolicy.Normalize()
	if policy.EarlyRetrySleepCap > 0 && sleepFor > policy.EarlyRetrySleepCap {
		sleepFor = policy.EarlyRetrySleepCap
		notReadyAfterSleep = true
	}
	if p.Metrics != nil {
		p.Metrics.IncRetryDelayed(msg.Headers[HeaderRetryReason])
	}
	p.Logger.Info(ctx, "retry message is not ready yet", "topic", msg.Topic, "offset", msg.Offset, "sleep_ms", sleepFor.Milliseconds())
	timer := time.NewTimer(sleepFor)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if notReadyAfterSleep {
			return fmt.Errorf("%w: retry_at=%s", ErrRetryNotReady, notBefore.Format(time.RFC3339Nano))
		}
		return nil
	}
}

func (p *Processor) errorResult(req contracts.RunRequest, status contracts.ExecutionStatus, decision RetryDecision, retryable bool) contracts.RunResult {
	return contracts.RunResult{
		SchemaVersion: contracts.RunResultSchemaVersion,
		SubmissionID:  req.SubmissionID,
		AttemptID:     req.AttemptID,
		UserID:        req.UserID,
		TaskID:        req.TaskID,
		Language:      req.Language,
		Status:        status,
		Summary:       contracts.ResultSummary{},
		Timing:        contracts.ResultTiming{},
		Resources:     contracts.ResultResources{},
		Logs:          contracts.ResultLogs{},
		Error: &contracts.RunError{
			Code:      decision.Reason,
			Message:   decision.Message,
			Retryable: retryable,
			Details: map[string]any{
				"class": string(decision.Class),
			},
		},
		TraceID:   req.TraceID,
		WorkerID:  p.WorkerID,
		CreatedAt: p.now(),
	}
}

func statusForErrorClass(class ErrorClass) contracts.ExecutionStatus {
	switch class {
	case ErrorClassValidation, ErrorClassUser:
		return contracts.StatusInvalidRequest
	default:
		return contracts.StatusInternalError
	}
}

func cloneHeaders(headers map[string]string) map[string]string {
	cloned := make(map[string]string, len(headers)+6)
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func (p *Processor) messageForResult(result contracts.RunResult) (Message, error) {
	return MessageForResult(p.Topics.Results, result, p.WorkerID)
}

func (p *Processor) messageForDeadLetter(deadLetter contracts.RunDeadLetter) (Message, error) {
	value, err := EncodeRunDeadLetter(deadLetter)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Topic:   p.Topics.DeadLetter,
		Key:     messageKey(deadLetter.SubmissionID, deadLetter.AttemptID, deadLetter.OriginalKey),
		Value:   value,
		Headers: resultHeaders(deadLetter.SchemaVersion, deadLetter.TraceID, p.WorkerID),
		Time:    p.now(),
	}, nil
}

func MessageForEvent(topic string, event contracts.RunEvent, workerID string) (Message, error) {
	value, err := EncodeRunEvent(event)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Topic:   topic,
		Key:     messageKey(event.SubmissionID, event.AttemptID, nil),
		Value:   value,
		Headers: resultHeaders(event.SchemaVersion, event.TraceID, workerID),
		Time:    event.CreatedAt,
	}, nil
}

func MessageForResult(topic string, result contracts.RunResult, workerID string) (Message, error) {
	value, err := EncodeRunResult(result)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Topic:   topic,
		Key:     messageKey(result.SubmissionID, result.AttemptID, nil),
		Value:   value,
		Headers: resultHeaders(result.SchemaVersion, result.TraceID, workerID),
		Time:    result.CreatedAt,
	}, nil
}

func messageKey(submissionID string, attemptID string, fallback *string) []byte {
	if submissionID != "" {
		return []byte(submissionID)
	}
	if attemptID != "" {
		return []byte(attemptID)
	}
	if fallback != nil {
		return []byte(*fallback)
	}
	return nil
}

func resultHeaders(schemaVersion string, traceID string, producer string) map[string]string {
	headers := map[string]string{
		HeaderSchemaVersion: schemaVersion,
	}
	if traceID != "" {
		headers[HeaderTraceID] = traceID
	}
	if producer != "" {
		headers[HeaderProducer] = producer
	}
	return headers
}

func traceIDFor(req contracts.RunRequest, msg Message) string {
	if req.TraceID != "" {
		return req.TraceID
	}
	return msg.Headers[HeaderTraceID]
}

func (p *Processor) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}
