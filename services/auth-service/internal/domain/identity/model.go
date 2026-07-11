package identity

import (
	"time"

	"github.com/google/uuid"
)

type Type string

const (
	TypeEmail Type = "email"
	TypePhone Type = "phone"
)

type Identity struct {
	ID              uuid.UUID
	Type            Type
	NormalizedValue string
	MaskedValue     string
	Status          string
	CredentialState string
}

type Link struct {
	ID         uuid.UUID
	Identity   uuid.UUID
	UserID     uuid.UUID
	Type       Type
	Status     string
	ExpiresAt  *time.Time
	PreviousID *uuid.UUID
}

type PasswordCredential struct {
	IdentityID uuid.UUID
	Hash       string
	Status     string
}
