package session

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	StatusRevoked       = "revoked"
	StatusReuseDetected = "reuse_detected"
)

var ErrInvalidStatusChange = errors.New("invalid session status change")

// StatusChange is the driver-free terminal state needed by status projections.
type StatusChange struct {
	SessionID  uuid.UUID
	UserID     uuid.UUID
	Status     string
	Version    int64
	ValidUntil time.Time
	OccurredAt time.Time
}

func (c StatusChange) Validate() error {
	if c.SessionID == uuid.Nil || c.UserID == uuid.Nil ||
		(c.Status != StatusRevoked && c.Status != StatusReuseDetected) ||
		c.Version < 0 || c.ValidUntil.IsZero() || c.OccurredAt.IsZero() {
		return ErrInvalidStatusChange
	}
	return nil
}
