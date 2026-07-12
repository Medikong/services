package reauth

import (
	"time"

	"github.com/google/uuid"
)

type Proof struct {
	ID            uuid.UUID
	Hash          []byte
	UserID        uuid.UUID
	SessionID     uuid.UUID
	IdentityID    *uuid.UUID
	Purpose       string
	ExpiresAt     time.Time
	ConsumedAt    *time.Time
	InvalidatedAt *time.Time
	Version       int64
	CreatedAt     time.Time
}

func (p Proof) Active(now time.Time) bool {
	return len(p.Hash) == 32 && p.UserID != uuid.Nil && p.SessionID != uuid.Nil && (p.Purpose == "link_identity" || p.Purpose == "replace_phone") && p.InvalidatedAt == nil && p.ConsumedAt == nil && p.ExpiresAt.After(now.UTC())
}
