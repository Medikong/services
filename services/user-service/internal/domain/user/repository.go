package user

import (
	"context"
	"errors"
)

var ErrUserNotFound = errors.New("user not found")

type Repository interface {
	Ensure(ctx context.Context, userID string) (User, error)
	Get(ctx context.Context, userID string) (User, error)
	UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) (User, error)
}
