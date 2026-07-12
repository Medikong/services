package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMarshalPayloadRedactsNestedSensitiveKeys(t *testing.T) {
	payload, err := MarshalPayload(map[string]any{
		"email": "buyer@example.com",
		"token": "secret-token",
		"nested": map[string]any{
			"authorization": "Bearer secret",
			"result":        "ok",
		},
	}, "email", "authorization")
	if err != nil {
		t.Fatalf("MarshalPayload() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded["email"] != "[REDACTED]" {
		t.Fatalf("email = %v, want redacted", decoded["email"])
	}
	if decoded["token"] != "[REDACTED]" {
		t.Fatalf("token = %v, want default redaction", decoded["token"])
	}
	nested := decoded["nested"].(map[string]any)
	if nested["authorization"] != "[REDACTED]" || nested["result"] != "ok" {
		t.Fatalf("nested payload = %#v", nested)
	}
}

func TestEventValidate(t *testing.T) {
	event := Event{
		ID:             uuid.New(),
		Name:           "resource.updated",
		Version:        1,
		OccurredAt:     time.Now().UTC(),
		Actor:          Actor{Type: "user", ID: "user-1"},
		Resource:       Resource{Type: "resource", ID: "resource-1"},
		Payload:        json.RawMessage(`{"result":"ok"}`),
		IdempotencyKey: "request-1",
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	event.IdempotencyKey = ""
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want missing idempotency key")
	}
}

func TestBackoffCapsAtMaximum(t *testing.T) {
	if got := backoff(1, time.Second, 10*time.Second); got != time.Second {
		t.Fatalf("backoff(1) = %s, want 1s", got)
	}
	if got := backoff(8, time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("backoff(8) = %s, want 10s", got)
	}
}
