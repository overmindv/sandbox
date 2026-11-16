package kafka

import (
	"errors"
	"time"

	"overmindv/sandbox/internal/contracts"
)

// attemptDeadline поддерживает старых producers без metadata; новые Tasks задают срок.
func attemptDeadline(req contracts.RunRequest) (time.Time, error) {
	raw, exists := req.Metadata["attempt_deadline"]
	if !exists {
		return time.Time{}, nil
	}
	value, ok := raw.(string)
	if !ok {
		return time.Time{}, errors.New("invalid attempt deadline")
	}
	deadline, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || deadline.IsZero() || !deadline.After(req.CreatedAt) {
		return time.Time{}, errors.New("invalid attempt deadline")
	}
	return deadline, nil
}

func deadlineDecision() RetryDecision {
	return RetryDecision{Class: ErrorClassInfra, Reason: "attempt_deadline_exceeded", Message: "attempt deadline exceeded"}
}
