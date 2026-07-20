package idempotency

import (
	"time"

	"github.com/google/uuid"
)

type Record struct {
	ID          uuid.UUID
	Operation   string
	ScopeHash   []byte
	KeyHash     []byte
	RequestHash []byte
	Status      string
	ResourceID  *uuid.UUID
	ReplayID    *uuid.UUID
	ExpiresAt   time.Time
}

// ReplayPayload holds the encrypted response of an explicitly allowlisted
// idempotent operation. Plain credentials never enter an idempotency record.
type ReplayPayload struct {
	ID          uuid.UUID
	Kind        string
	Ciphertext  []byte
	BindingHash []byte
	ReplayCount int16
	ExpiresAt   time.Time
	DestroyedAt *time.Time
}

func NewRecord(operation string, scopeHash, keyHash, requestHash []byte, resourceID, replayID *uuid.UUID, expiresAt time.Time) Record {
	return Record{
		ID:          uuid.New(),
		Operation:   operation,
		ScopeHash:   scopeHash,
		KeyHash:     keyHash,
		RequestHash: requestHash,
		ResourceID:  resourceID,
		ReplayID:    replayID,
		ExpiresAt:   expiresAt,
	}
}
