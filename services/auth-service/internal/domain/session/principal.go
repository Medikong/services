package session

import (
	"time"

	"github.com/google/uuid"
)

// Principal is the authenticated session context shared by auth use cases.
type Principal struct {
	Authenticated   bool
	SessionID       uuid.UUID
	UserID          uuid.UUID
	Channel         string
	Method          string
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
}
