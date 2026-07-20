package session

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	FindByWebSecret(context.Context, []byte) (Session, Credential, error)
	FindActive(context.Context, uuid.UUID) (Session, error)
	FindStatus(context.Context, uuid.UUID) (Session, error)
	// FindStatusForReconciliation waits for an in-flight status change on the
	// Session before returning the authoritative committed state.
	FindStatusForReconciliation(context.Context, uuid.UUID) (Session, error)
}
