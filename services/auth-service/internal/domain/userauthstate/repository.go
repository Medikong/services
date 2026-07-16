package userauthstate

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("user auth state not found")

type Repository interface {
	CreateActiveForRegistration(context.Context, pgx.Tx, uuid.UUID, int64, string) error
	FindForUpdate(context.Context, pgx.Tx, uuid.UUID) (State, error)
	Apply(context.Context, pgx.Tx, State, Change) (State, error)
}
