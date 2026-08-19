package observability

import "context"

type AuditLogger struct {
	enabled bool
	logger  *Logger
}

func NewAuditLogger(enabled bool, logger *Logger) *AuditLogger {
	if logger == nil {
		logger = NewLogger("info", "json", nil)
	}
	return &AuditLogger{enabled: enabled, logger: logger}
}

func (a *AuditLogger) SecurityReject(ctx context.Context, reason string, err error, attrs ...any) {
	if a == nil || !a.enabled {
		return
	}
	fields := []any{
		"audit_event", "security_reject",
		"reason", reason,
	}
	if err != nil {
		fields = append(fields, "error", err)
	}
	fields = append(fields, attrs...)
	a.logger.Warn(ctx, "security-relevant request rejected", fields...)
}
