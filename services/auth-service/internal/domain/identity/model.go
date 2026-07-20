package identity

import (
	"strings"
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

func NormalizePhone(value string) (string, error) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "-", "")
	if !strings.HasPrefix(value, "+") || len(value) < 8 {
		return "", ErrInvalidPhone
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return "", ErrInvalidPhone
		}
	}
	return value, nil
}

func MaskPhone(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:3] + strings.Repeat("*", len(value)-5) + value[len(value)-2:]
}
