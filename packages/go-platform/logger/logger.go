package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/samber/oops"
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
	if oopsErr, ok := oops.AsOops(err); ok {
		ctx, sensitiveValues := redactContext(oopsErr.Context())
		attrs := []any{slog.String("message", redactText(oopsErr.Error(), sensitiveValues))}
		if code := oopsErr.Code(); code != nil {
			attrs = append(attrs, slog.Any("code", code))
		}
		if domain := oopsErr.Domain(); domain != "" {
			attrs = append(attrs, slog.String("domain", domain))
		}
		if len(ctx) > 0 {
			attrs = append(attrs, slog.Any("context", ctx))
		}
		if stacktrace := oopsErr.Stacktrace(); stacktrace != "" {
			attrs = append(attrs, slog.String("stacktrace", redactText(stacktrace, sensitiveValues)))
		}
		return slog.Group("error", attrs...)
	}
	return slog.String("error", err.Error())
}

func redactContext(ctx map[string]any) (map[string]any, []string) {
	redacted := make(map[string]any, len(ctx))
	var sensitiveValues []string
	for key, value := range ctx {
		if isSensitiveKey(key) {
			collectStrings(reflect.ValueOf(value), 0, &sensitiveValues)
			redacted[key] = "[REDACTED]"
			continue
		}
		redacted[key] = redactNested(value, 0, &sensitiveValues)
	}
	return redacted, sensitiveValues
}

func redactNested(value any, depth int, sensitiveValues *[]string) any {
	if value == nil {
		return value
	}
	if depth >= 16 {
		return "[TRUNCATED]"
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return "[UNSUPPORTED]"
		}
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			item := iter.Value()
			if isSensitiveKey(key) {
				collectStrings(item, depth+1, sensitiveValues)
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactNested(item.Interface(), depth+1, sensitiveValues)
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = redactNested(rv.Index(i).Interface(), depth+1, sensitiveValues)
		}
		return out
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String:
		return value
	default:
		return "[UNSUPPORTED]"
	}
}

func collectStrings(value reflect.Value, depth int, values *[]string) {
	if !value.IsValid() || depth >= 16 {
		return
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		if text := value.String(); text != "" {
			*values = append(*values, text)
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			collectStrings(iter.Value(), depth+1, values)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			collectStrings(value.Index(i), depth+1, values)
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			collectStrings(value.Field(i), depth+1, values)
		}
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range []string{
		"authorization", "cookie", "password", "passwd", "secret", "token", "proof", "credential", "panic",
		"private_name", "privatename", "database_url", "databaseurl", "redis_url", "redisurl",
		"api_key", "apikey", "private_key", "privatekey", "signing_key", "signingkey", "encryption_key", "encryptionkey",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "key" || strings.HasSuffix(normalized, "_key")
}

func redactText(text string, sensitiveValues []string) string {
	for _, value := range sensitiveValues {
		text = strings.ReplaceAll(text, value, "[REDACTED]")
	}
	return text
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
