package observability

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

const TraceIDHeader = "X-Trace-Id"

type traceIDKey struct{}

type Logger struct {
	inner *slog.Logger
}

func NewLogger(level string, format string, output io.Writer) *Logger {
	if output == nil {
		output = os.Stdout
	}

	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var handler slog.Handler
	if format == "console" {
		handler = slog.NewTextHandler(output, opts)
	} else {
		handler = slog.NewJSONHandler(output, opts)
	}

	return &Logger{inner: slog.New(handler)}
}

func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelError, msg, args...)
}

func (l *Logger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if l == nil || l.inner == nil {
		return
	}
	if traceID := TraceIDFromContext(ctx); traceID != "" {
		args = append(args, "trace_id", safeTraceID(traceID))
	}
	safe := append([]any(nil), args...)
	for i := 1; i < len(safe); i += 2 {
		if err, ok := safe[i].(error); ok {
			safe[i] = fmt.Sprintf("%T", err)
		}
	}
	l.inner.Log(ctx, level, msg, safe...)
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = NewTraceID()
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(traceIDKey{}).(string)
	return value
}

func NewTraceID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "trace-unavailable"
	}
	return hex.EncodeToString(bytes[:])
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

var tracePattern = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

// Неизвестный формат correlation ID хешируется, чтобы он не стал каналом raw payload.
func safeTraceID(value string) string {
	if tracePattern.MatchString(value) {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
