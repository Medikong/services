package eventing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/google/uuid"
)

type OutboxItem struct {
	Envelope     policy.Envelope
	AttemptCount int
	TraceID      string
	LeaseOwner   string
	LeaseUntil   time.Time
}

type OutboxRepository interface {
	Claim(context.Context, string, int, time.Duration) ([]OutboxItem, error)
	MarkPublished(context.Context, uuid.UUID, string) error
	MarkFailed(context.Context, uuid.UUID, string, time.Time, string, bool) error
}

type Publisher interface {
	Publish(context.Context, policy.Envelope) error
}

type JSONData map[string]any

func decodeData(payload []byte) (JSONData, error) {
	var data JSONData
	err := json.Unmarshal(payload, &data)
	return data, err
}
