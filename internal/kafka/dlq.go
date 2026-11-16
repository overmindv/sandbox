package kafka

import (
	"time"

	"overmindv/sandbox/internal/contracts"
)

func NewDeadLetterFromMessage(msg Message, reason string, cause error, retryable bool, retryCount int, workerID string, failedAt time.Time) contracts.RunDeadLetter {
	// Обычный DLQ содержит только координаты; replay выполняется новой попыткой Tasks.
	var originalKey *string
	var payload *string
	message := "processing failed"

	originalTopic := msg.Topic
	originalPartition := msg.Partition
	originalOffset := msg.Offset

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
		OriginalHeaders:    map[string]string{},
		OriginalPayloadB64: payload,
		TraceID:            "",
		WorkerID:           workerID,
		FailedAt:           failedAt,
	}
}
