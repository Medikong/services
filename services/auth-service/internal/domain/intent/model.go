package intent

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Channel string

const (
	ChannelWeb    Channel = "web"
	ChannelMobile Channel = "mobile"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusConsumed Status = "consumed"
	StatusExpired  Status = "expired"
)

type Intent struct {
	ID             uuid.UUID
	Channel        Channel
	ReturnPath     string
	Type           string
	ActionContext  json.RawMessage
	OwnerProofHash []byte
	CSRFHash       []byte
	ExpiresAt      time.Time
	Status         Status
	RememberMe     *bool
}

type ActionPayload struct {
	ID          uuid.UUID
	IntentID    uuid.UUID
	ActionName  string
	Ciphertext  []byte
	ExpiresAt   time.Time
	DeliveredAt *time.Time
}

type CreateParams struct {
	ID              uuid.UUID
	Channel         Channel
	ReturnPath      string
	Type            string
	ActionContext   json.RawMessage
	OwnerProofHash  []byte
	CSRFHash        []byte
	ActionPayloadID *uuid.UUID
	ExpiresAt       time.Time
}
