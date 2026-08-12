package kafka

import (
	"encoding/base64"
	"time"

	"overmindv/sandbox/internal/contracts"
)

func NewDeadLetterFromMessage(msg Message, reason string, cause error, retryable bool, retryCount int, workerID string, failedAt time.Time) contracts.RunDeadLetter {
	var originalKey *string
	if len(msg.Key) > 0 {
		key := string(msg.Key)
		originalKey = &key
	}

	var payload *string
	if len(msg.Value) > 0 {
		encoded := base64.StdEncoding.EncodeToString(msg.Value)
		payload = &encoded
	}

	message := ""
	if cause != nil {
		message = cause.Error()
	}

	return contracts.RunDeadLetter{
		SchemaVersion:      contracts.RunDeadLetterSchemaVersion,
		Reason:             reason,
		Message:            message,
		Retryable:          retryable,
		RetryCount:         retryCount,
		OriginalTopic:      msg.Topic,
		OriginalPartition:  msg.Partition,
		OriginalOffset:     msg.Offset,
		OriginalKey:        originalKey,
		OriginalPayloadB64: payload,
		TraceID:            msg.Headers[HeaderTraceID],
		WorkerID:           workerID,
		FailedAt:           failedAt,
	}
}
