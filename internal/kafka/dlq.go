package kafka

import (
	"encoding/base64"
	"strconv"
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

	originalTopic := msg.Topic
	originalPartition := msg.Partition
	originalOffset := msg.Offset
	if value := msg.Headers[HeaderOriginalTopic]; value != "" {
		originalTopic = value
	}
	if value := msg.Headers[HeaderOriginalPartition]; value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			originalPartition = parsed
		}
	}
	if value := msg.Headers[HeaderOriginalOffset]; value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			originalOffset = parsed
		}
	}

	return contracts.RunDeadLetter{
		SchemaVersion:      contracts.RunDeadLetterSchemaVersion,
		Reason:             reason,
		Message:            message,
		Retryable:          retryable,
		RetryCount:         retryCount,
		OriginalTopic:      originalTopic,
		OriginalPartition:  originalPartition,
		OriginalOffset:     originalOffset,
		OriginalKey:        originalKey,
		OriginalHeaders:    cloneHeaders(msg.Headers),
		OriginalPayloadB64: payload,
		TraceID:            msg.Headers[HeaderTraceID],
		WorkerID:           workerID,
		FailedAt:           failedAt,
	}
}
