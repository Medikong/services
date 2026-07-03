package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
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

	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{
		AddSource:   config.addSource,
		Level:       config.level,
		ReplaceAttr: config.replaceAttr,
	})
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
