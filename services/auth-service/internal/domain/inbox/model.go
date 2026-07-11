package inbox

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusReceived  Status = "received"
	StatusProcessed Status = "processed"
	StatusRejected  Status = "rejected"
)

type Message struct {
	Consumer      string
	SourceEventID uuid.UUID
	Type          string
	SchemaVersion int16
	BusinessKey   uuid.UUID
	LinkRequestID uuid.UUID
	CausationID   uuid.UUID
	Payload       json.RawMessage
	PayloadHash   []byte
	Status        Status
	ReceivedAt    time.Time
}
