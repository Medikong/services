package audit

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
)

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Resource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Event struct {
	ID             uuid.UUID         `json:"id"`
	Name           string            `json:"name"`
	Version        int               `json:"version"`
	OccurredAt     time.Time         `json:"occurredAt"`
	Actor          Actor             `json:"actor"`
	Resource       Resource          `json:"resource"`
	Payload        json.RawMessage   `json:"payload"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

var defaultSensitivePayloadKeys = []string{
	"access_token",
	"api_key",
	"authorization",
	"cookie",
	"password",
	"refresh_token",
	"secret",
	"token",
}

func NewEvent(name string, version int, actor Actor, resource Resource, payload json.RawMessage, idempotencyKey string) Event {
	return Event{
		ID:             uuid.New(),
		Name:           name,
		Version:        version,
		OccurredAt:     time.Now().UTC(),
		Actor:          actor,
		Resource:       resource,
		Payload:        payload,
		Metadata:       map[string]string{},
		IdempotencyKey: idempotencyKey,
	}
}

func (e Event) Validate() error {
	errBuilder := oops.In("audit_event").Code("audit.invalid_event")
	switch {
	case e.ID == uuid.Nil:
		return errBuilder.New("audit event id is required")
	case strings.TrimSpace(e.Name) == "":
		return errBuilder.New("audit event name is required")
	case e.Version <= 0:
		return errBuilder.New("audit event version must be greater than zero")
	case e.OccurredAt.IsZero():
		return errBuilder.New("audit event occurred_at is required")
	case strings.TrimSpace(e.Actor.Type) == "" || strings.TrimSpace(e.Actor.ID) == "":
		return errBuilder.New("audit actor type and id are required")
	case strings.TrimSpace(e.Resource.Type) == "" || strings.TrimSpace(e.Resource.ID) == "":
		return errBuilder.New("audit resource type and id are required")
	case len(e.Payload) == 0 || !json.Valid(e.Payload):
		return errBuilder.New("audit payload must be valid JSON")
	case strings.TrimSpace(e.IdempotencyKey) == "":
		return errBuilder.New("audit idempotency key is required")
	}
	return nil
}

func MarshalPayload(value any, sensitiveKeys ...string) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, oops.In("audit_event").Code("audit.payload_encode_failed").Wrap(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, oops.In("audit_event").Code("audit.payload_decode_failed").Wrap(err)
	}
	keys := make(map[string]struct{}, len(defaultSensitivePayloadKeys)+len(sensitiveKeys))
	for _, key := range defaultSensitivePayloadKeys {
		keys[key] = struct{}{}
	}
	for _, key := range sensitiveKeys {
		if normalized := strings.ToLower(strings.TrimSpace(key)); normalized != "" {
			keys[normalized] = struct{}{}
		}
	}
	redact(decoded, keys)
	redacted, err := json.Marshal(decoded)
	if err != nil {
		return nil, oops.In("audit_event").Code("audit.payload_redact_failed").Wrap(err)
	}
	return redacted, nil
}

func redact(value any, keys map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, sensitive := keys[strings.ToLower(key)]; sensitive {
				typed[key] = "[REDACTED]"
				continue
			}
			redact(child, keys)
		}
	case []any:
		for _, child := range typed {
			redact(child, keys)
		}
	}
}
