package kafka

import "errors"

var (
	ErrMalformedMessage     = errors.New("malformed message")
	ErrInvalidRequest       = errors.New("invalid request")
	ErrNoRunRequestHandler  = errors.New("run request handler is not configured")
	ErrCommitBeforeHandling = errors.New("message committed before handling failed")
)

type RetryDecision struct {
	Retryable bool
	Reason    string
}

func NonRetryable(reason string) RetryDecision {
	return RetryDecision{Retryable: false, Reason: reason}
}

func Retryable(reason string) RetryDecision {
	return RetryDecision{Retryable: true, Reason: reason}
}
