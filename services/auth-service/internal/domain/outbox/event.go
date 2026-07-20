package outbox

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
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
