package intent

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Repository owns persistence for AuthenticationIntent only. Callers pass the
// pgx transaction that defines their use-case atomic boundary.
type Repository interface {
	Create(context.Context, pgx.Tx, CreateParams) error
	FindActiveForUpdate(context.Context, pgx.Tx, uuid.UUID) (Intent, error)
	RotateOwnerProof(context.Context, pgx.Tx, uuid.UUID, []byte, []byte) error
	SetRememberMe(context.Context, pgx.Tx, uuid.UUID, bool) error
	Consume(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error
	CreateActionPayload(context.Context, pgx.Tx, ActionPayload) error
	BindActionPayload(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
	FindConsumedActionForUpdate(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (Intent, ActionPayload, error)
	MarkActionDelivered(context.Context, pgx.Tx, uuid.UUID) error
}
