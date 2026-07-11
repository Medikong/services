package passwordreset

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusRequested         Status = "requested"
	StatusChallengeVerified Status = "challenge_verified"
	StatusCompleted         Status = "completed"
	StatusExpired           Status = "expired"
	StatusRevoked           Status = "revoked"
)

var (
	ErrInvalid    = errors.New("invalid password reset")
	ErrTransition = errors.New("invalid password reset transition")
)

// Reset is deliberately able to represent a decoy request with no Identity.
// This prevents account existence disclosure while retaining the same public
// lifecycle and challenge reference shape.
type Reset struct {
	ID                  uuid.UUID
	IntentID            *uuid.UUID
	IdentityID          *uuid.UUID
	ChallengeID         *uuid.UUID
	Status              Status
	ResetGrantHash      []byte
	ResetGrantKeyVer    *int16
	PolicyVersion       *int64
	ExpiresAt           time.Time
	ChallengeVerifiedAt *time.Time
	CompletedAt         *time.Time
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func New(id uuid.UUID, intentID, identityID *uuid.UUID, expiresAt, now time.Time) (Reset, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r := Reset{ID: id, IntentID: copyUUID(intentID), IdentityID: copyUUID(identityID), Status: StatusRequested, ExpiresAt: expiresAt.UTC(), CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := r.Validate(); err != nil {
		return Reset{}, err
	}
	return r, nil
}

func (r *Reset) AttachChallenge(id uuid.UUID) error {
	if r.Status != StatusRequested || id == uuid.Nil {
		return ErrTransition
	}
	r.ChallengeID = copyUUID(&id)
	return nil
}

func (r *Reset) Verify(grantHash []byte, now time.Time) error {
	if r.Status != StatusRequested || r.ChallengeID == nil || len(grantHash) != 32 {
		return ErrTransition
	}
	if !now.UTC().Before(r.ExpiresAt) {
		r.Status = StatusExpired
		return ErrTransition
	}
	r.Status, r.ResetGrantHash = StatusChallengeVerified, append([]byte(nil), grantHash...)
	version := int16(1)
	r.ResetGrantKeyVer = &version
	verifiedAt := now.UTC()
	r.ChallengeVerifiedAt = &verifiedAt
	return nil
}

func (r *Reset) Complete(now time.Time) error {
	if r.Status != StatusChallengeVerified || r.IdentityID == nil {
		return ErrTransition
	}
	if !now.UTC().Before(r.ExpiresAt) {
		r.Status = StatusExpired
		return ErrTransition
	}
	r.Status = StatusCompleted
	completedAt := now.UTC()
	r.CompletedAt = &completedAt
	return nil
}

func (r Reset) Validate() error {
	if r.ID == uuid.Nil || r.ExpiresAt.IsZero() || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.Version < 0 {
		return ErrInvalid
	}
	if !r.ExpiresAt.After(r.CreatedAt) {
		return ErrInvalid
	}
	switch r.Status {
	case StatusRequested:
	case StatusChallengeVerified:
		if r.ChallengeID == nil || len(r.ResetGrantHash) != 32 || r.ResetGrantKeyVer == nil || r.ChallengeVerifiedAt == nil {
			return ErrInvalid
		}
	case StatusCompleted:
		if r.IdentityID == nil || r.CompletedAt == nil {
			return ErrInvalid
		}
	case StatusExpired, StatusRevoked:
	default:
		return ErrInvalid
	}
	return nil
}

func copyUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
