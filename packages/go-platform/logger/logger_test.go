package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestNewReturnsSlogLoggerWithService(t *testing.T) {
	var out bytes.Buffer
	log := New(&out, "test-service")

	log.InfoContext(context.Background(), "started", slog.String("addr", ":8080"))

	event := decodeEvent(t, out.Bytes())
	if event["service"] != "test-service" {
		t.Fatalf("service = %v, want test-service", event["service"])
	}
	if event["msg"] != "started" {
		t.Fatalf("msg = %v, want started", event["msg"])
	}
	if event["addr"] != ":8080" {
		t.Fatalf("addr = %v, want :8080", event["addr"])
	}
}

func TestPackageLevelLoggerUsesConfiguredDefault(t *testing.T) {
	var out bytes.Buffer
	original := Default()
	t.Cleanup(func() {
		SetDefault(original)
	})

	Configure(&out, "global-service")

	Info(context.Background(), "ready", "path", "/readyz")
	Error(context.Background(), "failed", Err(errors.New("boom")))

	events := decodeEvents(t, out.Bytes())
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0]["service"] != "global-service" || events[0]["path"] != "/readyz" {
		t.Fatalf("unexpected info event: %#v", events[0])
	}
	if events[1]["level"] != "ERROR" || events[1]["error"] != "boom" {
		t.Fatalf("unexpected error event: %#v", events[1])
	}
}

func TestWithLevelControlsOutput(t *testing.T) {
	var out bytes.Buffer
	log := New(&out, "debug-service", WithLevel(slog.LevelDebug))

	log.DebugContext(context.Background(), "debug enabled")

	event := decodeEvent(t, out.Bytes())
	if event["level"] != "DEBUG" {
		t.Fatalf("level = %v, want DEBUG", event["level"])
	}
}

func TestNewAddsTraceContext(t *testing.T) {
	var out bytes.Buffer
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, span := provider.Tracer("test").Start(context.Background(), "operation")
	defer span.End()

	New(&out, "trace-service").InfoContext(ctx, "traced")

	event := decodeEvent(t, out.Bytes())
	if event["trace_id"] != span.SpanContext().TraceID().String() {
		t.Fatalf("trace_id = %v, want %s", event["trace_id"], span.SpanContext().TraceID())
	}
	if event["span_id"] != span.SpanContext().SpanID().String() {
		t.Fatalf("span_id = %v, want %s", event["span_id"], span.SpanContext().SpanID())
	}
}

func TestRedactKeys(t *testing.T) {
	var out bytes.Buffer
	log := New(&out, "redact-service", WithReplaceAttr(RedactKeys("password", "authorization")))

	log.Info("request", "password", "secret", "Authorization", "Bearer token", "result", "ok")

	event := decodeEvent(t, out.Bytes())
	if event["password"] != "[REDACTED]" || event["Authorization"] != "[REDACTED]" {
		t.Fatalf("sensitive values were not redacted: %#v", event)
	}
	if event["result"] != "ok" {
		t.Fatalf("non-sensitive value = %v, want ok", event["result"])
	}
}

func decodeEvent(t *testing.T, data []byte) map[string]any {
	t.Helper()
	events := decodeEvents(t, data)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	return events[0]
}

func decodeEvents(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	var events []map[string]any
	for {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode event: %v", err)
		}
		events = append(events, event)
	}
	return events
}
