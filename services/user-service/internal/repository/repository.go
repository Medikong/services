package repository

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/user-service/internal/model"
)

var ErrUserNotFound = errors.New("user not found")

type Store interface {
	Ensure(ctx context.Context, userID string) (model.User, error)
	Get(ctx context.Context, userID string) (model.User, error)
	UpdateProfile(ctx context.Context, userID string, update model.ProfileUpdate) (model.User, error)
}
