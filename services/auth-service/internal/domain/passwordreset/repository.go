package passwordreset

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound        = errors.New("password reset not found")
	ErrVersionConflict = errors.New("password reset version conflict")
)

type Repository interface {
	Create(context.Context, pgx.Tx, Reset) error
	FindForUpdate(context.Context, pgx.Tx, uuid.UUID) (Reset, error)
	Save(context.Context, pgx.Tx, *Reset) error
}
