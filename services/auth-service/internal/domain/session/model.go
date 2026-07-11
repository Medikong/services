package session

import (
	"time"

	"github.com/google/uuid"
)

type Channel string

const (
	ChannelWeb    Channel = "web"
	ChannelMobile Channel = "mobile"
)

type Session struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	IdentityID      uuid.UUID
	IdentityLink    uuid.UUID
	Method          string
	Channel         Channel
	RememberMe      bool
	Roles           []string
	GrantID         uuid.UUID
	GrantVersion    int64
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
	Status          string
}

type Credential struct {
	ID                        uuid.UUID
	SessionID                 uuid.UUID
	Type                      string
	Status                    string
	SecretHash                []byte
	FamilyID                  *uuid.UUID
	ExpiresAt                 time.Time
	DeliveryRecoveryExpiresAt *time.Time
}

type CreateParams struct {
	Session    Session
	Credential Credential
}
