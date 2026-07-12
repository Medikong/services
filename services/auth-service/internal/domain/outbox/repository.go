package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	ID            uuid.UUID
	Type          string
	AggregateType string
	AggregateID   uuid.UUID
	Version       int64
	Payload       json.RawMessage
	CorrelationID uuid.UUID
	OccurredAt    time.Time
}

type ClaimedEvent struct {
	Event
	Attempts int
}

// Repository is the durable domain-event outbox. External provider/topic
// addresses intentionally do not live here; a worker/adapter owns delivery.
type Repository interface {
	Append(context.Context, pgx.Tx, Event) error
	ClaimPublishBatch(context.Context, string, int, time.Duration) ([]ClaimedEvent, error)
	MarkPublished(context.Context, uuid.UUID, string) error
	ReleaseForRetry(context.Context, uuid.UUID, string, time.Duration, string) error
	MarkDeadLetter(context.Context, uuid.UUID, string, string) error
}
