package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/observability"
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

type Processor struct {
	Consumer          Consumer
	Producer          Producer
	Topics            TopicNames
	WorkerID          string
	CommitPolicy      CommitPolicy
	ValidationOptions contracts.ValidationOptions
	Handler           RunRequestHandler
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
		Handler:           handler,
		Now:               func() time.Time { return time.Now().UTC() },
		Logger:            logger,
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

func (p *Processor) ProcessMessage(ctx context.Context, msg Message) error {
	traceID := msg.Headers[HeaderTraceID]
	ctx = observability.WithTraceID(ctx, traceID)

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

	if p.Handler == nil {
		return ErrNoRunRequestHandler
	}
	return p.Handler.HandleRunRequest(ctx, msg, req)
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
	return p.Producer.WriteMessages(ctx, deadLetterMessage)
}

func (p *Processor) messageForResult(result contracts.RunResult) (Message, error) {
	value, err := EncodeRunResult(result)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Topic:   p.Topics.Results,
		Key:     messageKey(result.AttemptID, nil),
		Value:   value,
		Headers: resultHeaders(result.SchemaVersion, result.TraceID, p.WorkerID),
		Time:    p.now(),
	}, nil
}

func (p *Processor) messageForDeadLetter(deadLetter contracts.RunDeadLetter) (Message, error) {
	value, err := EncodeRunDeadLetter(deadLetter)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Topic:   p.Topics.DeadLetter,
		Key:     messageKey(deadLetter.AttemptID, deadLetter.OriginalKey),
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
		Key:     messageKey(event.AttemptID, nil),
		Value:   value,
		Headers: resultHeaders(event.SchemaVersion, event.TraceID, workerID),
		Time:    event.CreatedAt,
	}, nil
}

func messageKey(attemptID string, fallback *string) []byte {
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
