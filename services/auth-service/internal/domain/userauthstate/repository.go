package userauthstate

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("user auth state not found")

type Repository interface {
	CreateActiveForRegistration(context.Context, uuid.UUID, int64, string) error
	FindForUpdate(context.Context, uuid.UUID) (State, error)
	Apply(context.Context, State, Change) (State, error)
}
