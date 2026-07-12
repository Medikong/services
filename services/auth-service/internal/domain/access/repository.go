package access

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	CreateActiveForRegistration(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
	FindActiveForUpdate(context.Context, pgx.Tx, uuid.UUID) (State, Grant, error)
	FindActive(context.Context, uuid.UUID) (State, Grant, error)
	Restrict(context.Context, pgx.Tx, uuid.UUID, string, int64) error
}
