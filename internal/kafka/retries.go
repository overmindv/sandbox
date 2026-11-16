package kafka

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"overmindv/sandbox/internal/config"
)

var (
	ErrMalformedMessage     = errors.New("malformed message")
	ErrInvalidRequest       = errors.New("invalid request")
	ErrNoRunRequestHandler  = errors.New("run request handler is not configured")
	ErrCommitBeforeHandling = errors.New("message committed before handling failed")
	ErrRetryNotReady        = errors.New("retry message is not ready")
)

type ErrorClass string

const (
	ErrorClassValidation ErrorClass = "validation"
	ErrorClassUser       ErrorClass = "user"
	ErrorClassInfra      ErrorClass = "infra"
	ErrorClassInternal   ErrorClass = "internal"
)

type ProcessingError struct {
	Class          ErrorClass
	Reason         string
	Retryable      bool
	ReleaseAttempt bool
	Cause          error
}

func NewProcessingError(class ErrorClass, reason string, cause error, retryable bool) ProcessingError {
	if reason == "" {
		reason = "processing_error"
	}
	return ProcessingError{
		Class:     class,
		Reason:    reason,
		Retryable: retryable,
		Cause:     cause,
	}
}

func NewRetryableProcessingError(class ErrorClass, reason string, cause error, releaseAttempt bool) ProcessingError {
	err := NewProcessingError(class, reason, cause, true)
	err.ReleaseAttempt = releaseAttempt
	return err
}

func (e ProcessingError) Error() string {
	if e.Cause == nil {
		return e.Reason
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Cause)
}

func (e ProcessingError) Unwrap() error {
	return e.Cause
}

type RetryDecision struct {
	Class          ErrorClass
	Reason         string
	Retryable      bool
	ReleaseAttempt bool
	Message        string
}

func NonRetryable(reason string) RetryDecision {
	return RetryDecision{Class: ErrorClassUser, Retryable: false, Reason: reason, Message: reason}
}

func Retryable(reason string) RetryDecision {
	return RetryDecision{Class: ErrorClassInfra, Retryable: true, Reason: reason, Message: reason}
}

type RetryPolicy struct {
	Enabled            bool
	MaxAttempts        int
	InitialBackoff     time.Duration
	MaxBackoff         time.Duration
	BackoffMultiplier  float64
	EarlyRetrySleepCap time.Duration
}

func RetryPolicyFromConfig(cfg config.KafkaRetryConfig) RetryPolicy {
	policy := RetryPolicy{
		Enabled:            cfg.Enabled,
		MaxAttempts:        cfg.MaxAttempts,
		InitialBackoff:     time.Duration(cfg.InitialBackoffMS) * time.Millisecond,
		MaxBackoff:         time.Duration(cfg.MaxBackoffMS) * time.Millisecond,
		BackoffMultiplier:  cfg.BackoffMultiplier,
		EarlyRetrySleepCap: time.Duration(cfg.EarlyRetrySleepCapMS) * time.Millisecond,
	}
	return policy.Normalize()
}

func (p RetryPolicy) Normalize() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.MaxAttempts > 5 {
		p.MaxAttempts = 5
	}
	if p.InitialBackoff > 30*time.Second {
		p.InitialBackoff = 30 * time.Second
	}
	if p.MaxBackoff > 30*time.Second {
		p.MaxBackoff = 30 * time.Second
	}
	if p.InitialBackoff < 0 {
		p.InitialBackoff = 0
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = p.InitialBackoff
	}
	if p.MaxBackoff < p.InitialBackoff {
		p.MaxBackoff = p.InitialBackoff
	}
	if p.BackoffMultiplier < 1 {
		p.BackoffMultiplier = 1
	}
	if p.EarlyRetrySleepCap < 0 {
		p.EarlyRetrySleepCap = 0
	}
	return p
}

func (p RetryPolicy) HandlingAttempt(retryCount int) int {
	if retryCount < 0 {
		retryCount = 0
	}
	return retryCount + 1
}

func (p RetryPolicy) Exhausted(retryCount int) bool {
	p = p.Normalize()
	return p.HandlingAttempt(retryCount) >= p.MaxAttempts
}

func (p RetryPolicy) NextBackoff(retryCount int) time.Duration {
	p = p.Normalize()
	if p.InitialBackoff == 0 {
		return 0
	}
	if retryCount < 0 {
		retryCount = 0
	}
	multiplier := math.Pow(p.BackoffMultiplier, float64(retryCount))
	value := float64(p.InitialBackoff) * multiplier
	if math.IsInf(value, 0) || value >= float64(p.MaxBackoff) {
		return p.MaxBackoff
	}
	backoff := time.Duration(value)
	if backoff > p.MaxBackoff {
		return p.MaxBackoff
	}
	return backoff
}

func ClassifyProcessingError(err error) RetryDecision {
	if err == nil {
		return RetryDecision{}
	}

	var processingErr ProcessingError
	if errors.As(err, &processingErr) {
		reason := normalizeReason(processingErr.Reason)
		if reason == "" {
			reason = "processing_error"
		}
		message := strings.TrimSpace(processingErr.Error())
		if message == "" {
			message = reason
		}
		class := processingErr.Class
		if class == "" {
			class = ErrorClassInternal
		}
		return RetryDecision{
			Class:          class,
			Reason:         reason,
			Retryable:      processingErr.Retryable,
			ReleaseAttempt: processingErr.ReleaseAttempt,
			Message:        message,
		}
	}

	if errors.Is(err, context.Canceled) {
		return RetryDecision{Class: ErrorClassInfra, Reason: "context_canceled", Retryable: false, Message: err.Error()}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return RetryDecision{Class: ErrorClassInfra, Reason: "deadline_exceeded", Retryable: true, ReleaseAttempt: true, Message: err.Error()}
	}
	if errors.Is(err, ErrNoRunRequestHandler) {
		return RetryDecision{Class: ErrorClassInternal, Reason: "handler_not_configured", Retryable: true, Message: err.Error()}
	}

	return RetryDecision{
		Class:          ErrorClassInternal,
		Reason:         "internal_error",
		Retryable:      true,
		ReleaseAttempt: true,
		Message:        err.Error(),
	}
}

func RetryCount(headers map[string]string) int {
	if len(headers) == 0 {
		return 0
	}
	value := strings.TrimSpace(headers[HeaderRetryCount])
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func RetryNotBefore(headers map[string]string) (time.Time, bool) {
	if len(headers) == 0 {
		return time.Time{}, false
	}
	value := strings.TrimSpace(headers[HeaderRetryNotBefore])
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func normalizeReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range reason {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
