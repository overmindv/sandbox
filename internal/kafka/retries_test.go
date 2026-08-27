package kafka

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryPolicyBackoffAndExhaustion(t *testing.T) {
	policy := RetryPolicy{
		Enabled:           true,
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        time.Second,
		BackoffMultiplier: 2,
	}.Normalize()

	if policy.NextBackoff(0) != 100*time.Millisecond {
		t.Errorf("expected first backoff 100ms, got %s", policy.NextBackoff(0))
	}
	if policy.NextBackoff(1) != 200*time.Millisecond {
		t.Errorf("expected second backoff 200ms, got %s", policy.NextBackoff(1))
	}
	if policy.Exhausted(1) {
		t.Error("expected retry_count=1 not to be exhausted for max_attempts=3")
	}
	if !policy.Exhausted(2) {
		t.Error("expected retry_count=2 to be exhausted for max_attempts=3")
	}
}

func TestClassifyProcessingError(t *testing.T) {
	err := NewRetryableProcessingError(ErrorClassInfra, "gojudge_unavailable", errors.New("dial tcp failed"), true)
	decision := ClassifyProcessingError(err)
	if decision.Class != ErrorClassInfra {
		t.Errorf("expected infra class, got %q", decision.Class)
	}
	if !decision.Retryable {
		t.Error("expected retryable decision")
	}
	if !decision.ReleaseAttempt {
		t.Error("expected release attempt decision")
	}
	if decision.Reason != "gojudge_unavailable" {
		t.Errorf("expected normalized reason, got %q", decision.Reason)
	}

	decision = ClassifyProcessingError(context.DeadlineExceeded)
	if decision.Class != ErrorClassInfra || !decision.Retryable || !decision.ReleaseAttempt {
		t.Errorf("expected retryable deadline infra decision, got %#v", decision)
	}
}

func TestRetryHeadersRoundTrip(t *testing.T) {
	notBefore := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	headers := map[string]string{
		HeaderRetryCount:     "2",
		HeaderRetryNotBefore: notBefore.Format(time.RFC3339Nano),
	}

	if RetryCount(headers) != 2 {
		t.Errorf("expected retry count 2, got %d", RetryCount(headers))
	}
	parsed, ok := RetryNotBefore(headers)
	if !ok {
		t.Error("expected retry not-before header to parse")
		return
	}
	if !parsed.Equal(notBefore) {
		t.Errorf("expected not-before %s, got %s", notBefore, parsed)
	}
}
