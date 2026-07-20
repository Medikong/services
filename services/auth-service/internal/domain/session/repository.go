package session

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	FindByWebSecret(context.Context, []byte) (Session, Credential, error)
	FindActive(context.Context, uuid.UUID) (Session, error)
	FindStatus(context.Context, uuid.UUID) (Session, error)
}
