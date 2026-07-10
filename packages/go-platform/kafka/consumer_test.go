package kafka

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
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
