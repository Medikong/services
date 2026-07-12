package reauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("reauthentication proof not found")

type Repository interface {
	Create(context.Context, pgx.Tx, Proof) error
	FindActiveForUpdate(context.Context, pgx.Tx, []byte, uuid.UUID, uuid.UUID, string) (Proof, error)
	Consume(context.Context, pgx.Tx, uuid.UUID) error
}
