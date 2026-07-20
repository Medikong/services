package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-platform/logger"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestKafkaPublishLogUsesCorrelationFieldsWithoutPayload(t *testing.T) {
	var output bytes.Buffer
	original := logger.Default()
	logger.Configure(&output, "auth-service")
	t.Cleanup(func() { logger.SetDefault(original) })

	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, span := provider.Tracer("test").Start(context.Background(), "publish auth event")
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	publisher := &KafkaPublisher{
		service:     "auth-service",
		version:     "test-version",
		environment: "test",
		topic:       "auth.events",
	}
	event := domainoutbox.Event{
		ID:            uuid.New(),
		Type:          "auth.registration.completed",
		AggregateType: "user",
		AggregateID:   uuid.New(),
		Version:       1,
		Payload:       json.RawMessage(`{"password":"must-not-be-logged"}`),
		CorrelationID: uuid.New(),
		OccurredAt:    time.Now(),
	}

	publisher.logPublish(ctx, event, "success")
	span.End()

	var logEvent map[string]any
	if err := json.Unmarshal(output.Bytes(), &logEvent); err != nil {
		t.Fatalf("decode Kafka publish log: %v", err)
	}
	for key, expected := range map[string]any{
		"event":                      "kafka.message.publish",
		"service.name":               "auth-service",
		"service.version":            "test-version",
		"service.environment":        "test",
		"messaging.system":           "kafka",
		"messaging.operation":        "publish",
		"messaging.destination.name": "auth.events",
		"messaging.message.type":     event.Type,
		"correlation_id":             event.CorrelationID.String(),
		"trace_id":                   span.SpanContext().TraceID().String(),
		"span_id":                    span.SpanContext().SpanID().String(),
		"outcome":                    "success",
		"log.kind":                   "messaging",
		"log.policy":                 "keep",
	} {
		if logEvent[key] != expected {
			t.Fatalf("%s = %#v, want %#v", key, logEvent[key], expected)
		}
	}
	for _, forbidden := range []string{"must-not-be-logged", "password", event.ID.String(), event.AggregateID.String()} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("Kafka publish log leaked %q: %s", forbidden, output.String())
		}
	}
}

func TestKafkaPublishFailureLogUsesFailureOutcomeAndErrorLevel(t *testing.T) {
	var output bytes.Buffer
	original := logger.Default()
	logger.Configure(&output, "auth-service")
	t.Cleanup(func() { logger.SetDefault(original) })

	publisher := &KafkaPublisher{
		service:     "auth-service",
		version:     "test-version",
		environment: "test",
		topic:       "auth.events",
	}
	event := domainoutbox.Event{
		ID:            uuid.New(),
		Type:          "auth.registration.completed",
		AggregateID:   uuid.New(),
		Payload:       json.RawMessage(`{"secret":"must-not-be-logged"}`),
		CorrelationID: uuid.New(),
	}

	publisher.logPublish(context.Background(), event, "failure")

	var logEvent map[string]any
	if err := json.Unmarshal(output.Bytes(), &logEvent); err != nil {
		t.Fatalf("decode Kafka failure log: %v", err)
	}
	if logEvent["level"] != "ERROR" || logEvent["outcome"] != "failure" {
		t.Fatalf("Kafka failure log = %#v", logEvent)
	}
	for _, forbidden := range []string{"must-not-be-logged", "secret", event.ID.String(), event.AggregateID.String()} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("Kafka failure log leaked %q: %s", forbidden, output.String())
		}
	}
}
