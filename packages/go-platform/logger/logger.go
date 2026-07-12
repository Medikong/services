package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/trace"
)

type Option func(*options)

type options struct {
	addSource   bool
	level       slog.Leveler
	replaceAttr func(groups []string, attr slog.Attr) slog.Attr
}

var (
	defaultMu     sync.RWMutex
	defaultLogger = New(os.Stdout, "")
)

func New(out io.Writer, service string, opts ...Option) *slog.Logger {
	if out == nil {
		panic("logger: nil writer")
	}

	config := options{
		level: slog.LevelInfo,
	}
	for _, opt := range opts {
		opt(&config)
	}

	handler := traceHandler{Handler: slog.NewJSONHandler(out, &slog.HandlerOptions{
		AddSource:   config.addSource,
		Level:       config.level,
		ReplaceAttr: config.replaceAttr,
	})}
	log := slog.New(handler)
	if service != "" {
		log = log.With(slog.String("service", service))
	}
	return log
}

func Configure(out io.Writer, service string, opts ...Option) *slog.Logger {
	log := New(out, service, opts...)
	SetDefault(log)
	return log
}

func SetDefault(log *slog.Logger) {
	if log == nil {
		panic("logger: nil default logger")
	}

	defaultMu.Lock()
	defaultLogger = log
	defaultMu.Unlock()
	slog.SetDefault(log)
}

func Default() *slog.Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultLogger
}

func WithLevel(level slog.Leveler) Option {
	return func(opts *options) {
		opts.level = level
	}
}

func WithAddSource(enabled bool) Option {
	return func(opts *options) {
		opts.addSource = enabled
	}
}

func WithReplaceAttr(replace func(groups []string, attr slog.Attr) slog.Attr) Option {
	return func(opts *options) {
		opts.replaceAttr = replace
	}
}

func RedactKeys(keys ...string) func([]string, slog.Attr) slog.Attr {
	redacted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if normalized := strings.ToLower(strings.TrimSpace(key)); normalized != "" {
			redacted[normalized] = struct{}{}
		}
	}
	return func(_ []string, attr slog.Attr) slog.Attr {
		if _, ok := redacted[strings.ToLower(attr.Key)]; ok {
			return slog.String(attr.Key, "[REDACTED]")
		}
		return attr
	}
}

func Debug(ctx context.Context, msg string, args ...any) {
	Default().DebugContext(ctx, msg, args...)
}

func Info(ctx context.Context, msg string, args ...any) {
	Default().InfoContext(ctx, msg, args...)
}

func Warn(ctx context.Context, msg string, args ...any) {
	Default().WarnContext(ctx, msg, args...)
}

func Error(ctx context.Context, msg string, args ...any) {
	Default().ErrorContext(ctx, msg, args...)
}

func With(args ...any) *slog.Logger {
	return Default().With(args...)
}

func Err(err error) slog.Attr {
	if err == nil {
		return slog.Any("error", nil)
	}
	return slog.String("error", err.Error())
}

type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", span.TraceID().String()),
			slog.String("span_id", span.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{Handler: h.Handler.WithGroup(name)}
}
