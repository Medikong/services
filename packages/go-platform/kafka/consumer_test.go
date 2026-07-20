package kafka

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func TestRecordCarrierReplacesAndAppendsHeaders(t *testing.T) {
	record := &kgo.Record{Headers: []kgo.RecordHeader{{Key: "traceparent", Value: []byte("old")}}}
	carrier := recordCarrier{record: record}

	carrier.Set("traceparent", "new")
	carrier.Set("baggage", "request_id=req-1")

	if got := carrier.Get("traceparent"); got != "new" {
		t.Fatalf("traceparent = %q, want new", got)
	}
	if got := carrier.Get("baggage"); got != "request_id=req-1" {
		t.Fatalf("baggage = %q, want request_id", got)
	}
	if len(carrier.Keys()) != 2 {
		t.Fatalf("keys = %v, want 2 headers", carrier.Keys())
	}
}

func TestRecordID(t *testing.T) {
	record := &kgo.Record{Topic: "orders", Partition: 2, Offset: 42}
	if got := RecordID(record); got != "orders:2:42" {
		t.Fatalf("RecordID() = %q", got)
	}
}

func TestInjectAddsW3CTraceContextWithoutMessagePayload(t *testing.T) {
	original := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(original) })

	remote := propagation.TraceContext{}.Extract(
		context.Background(),
		propagation.MapCarrier{"traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01"},
	)
	record := &kgo.Record{Topic: "auth-events", Value: []byte("private-payload")}

	Inject(remote, record)

	if got := (recordCarrier{record: record}).Get("traceparent"); got == "" {
		t.Fatal("traceparent header was not injected")
	}
	if string(record.Value) != "private-payload" {
		t.Fatal("Inject() modified message payload")
	}
}
