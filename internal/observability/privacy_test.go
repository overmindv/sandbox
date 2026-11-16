package observability

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestLoggerDoesNotExposeErrorPayload(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger("debug", "json", &output)
	logger.Error(WithTraceID(context.Background(), "SECRET-TRACE"), "execution failed", "error", errors.New("SECRET hidden input"))
	NewAuditLogger(true, logger).SecurityReject(context.Background(), "invalid_request", errors.New("SECRET compiler path"))
	if strings.Contains(output.String(), "SECRET") {
		t.Fatalf("logs leaked: %s", output.String())
	}
}
