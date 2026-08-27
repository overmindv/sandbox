package observability

import (
	"context"
	"sync"
	"time"
)

type Tracer struct {
	enabled bool
	logger  *Logger
	now     func() time.Time
}

func NewTracer(enabled bool, logger *Logger) *Tracer {
	if logger == nil {
		logger = NewLogger("info", "json", nil)
	}
	return &Tracer{
		enabled: enabled,
		logger:  logger,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (t *Tracer) Start(ctx context.Context, name string, attrs ...any) (context.Context, func(error, ...any)) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil || !t.enabled {
		return ctx, func(error, ...any) {}
	}
	if TraceIDFromContext(ctx) == "" {
		ctx = WithTraceID(ctx, "")
	}

	startedAt := t.now()
	startAttrs := []any{
		"span", name,
		"span_event", "start",
	}
	startAttrs = append(startAttrs, attrs...)
	t.logger.Debug(ctx, "trace span started", startAttrs...)

	var once sync.Once
	return ctx, func(err error, endAttrs ...any) {
		once.Do(func() {
			endedAt := t.now()
			fields := []any{
				"span", name,
				"span_event", "end",
				"duration_ms", endedAt.Sub(startedAt).Milliseconds(),
			}
			fields = append(fields, attrs...)
			fields = append(fields, endAttrs...)
			if err != nil {
				fields = append(fields, "error", err)
				t.logger.Warn(ctx, "trace span ended", fields...)
				return
			}
			t.logger.Info(ctx, "trace span ended", fields...)
		})
	}
}
