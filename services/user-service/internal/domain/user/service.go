package user

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-authz/principal"
)

type Service struct {
	store Repository
}

func NewService(store Repository) Service {
	return Service{store: store}
}

type EnsureInput struct {
	UserID string `json:"userId"`
}

func (s Service) Ensure(ctx context.Context, input EnsureInput) (User, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return User{}, ErrMissingUserID
	}
	return s.store.Ensure(ctx, strings.TrimSpace(input.UserID))
}

func (s Service) Me(ctx context.Context, p principal.Principal) (User, error) {
	if p.Type != principal.TypeUser || strings.TrimSpace(p.UserID) == "" {
		return User{}, ErrUnauthorized
	}
	return s.store.Ensure(ctx, p.UserID)
}

func (s Service) Get(ctx context.Context, p principal.Principal, userID string) (User, error) {
	if p.Type != principal.TypeUser {
		return User{}, ErrUnauthorized
	}
	if p.UserID != userID && !p.HasRole("operator") {
		return User{}, ErrForbidden
	}
	return s.store.Get(ctx, userID)
}

func (s Service) UpdateMyProfile(ctx context.Context, p principal.Principal, input ProfileUpdate) (User, error) {
	if p.Type != principal.TypeUser || strings.TrimSpace(p.UserID) == "" {
		return User{}, ErrUnauthorized
	}
	if err := validateProfileUpdate(input); err != nil {
		return User{}, err
	}
	if _, err := s.store.Ensure(ctx, p.UserID); err != nil {
		return User{}, err
	}
	return s.store.UpdateProfile(ctx, p.UserID, input)
}

func validateProfileUpdate(input ProfileUpdate) error {
	if input.RealName != nil && strings.TrimSpace(*input.RealName) == "" {
		return ErrInvalidProfile
	}
	if input.Nickname != nil {
		nickname := strings.TrimSpace(*input.Nickname)
		if nickname == "" || len(nickname) > 40 {
			return ErrInvalidProfile
		}
	}
	if input.ProfileIcon != nil && len(strings.TrimSpace(*input.ProfileIcon)) > 200 {
		return ErrInvalidProfile
	}
	return nil
}

var (
	ErrMissingUserID  = errors.New("missing user id")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
	ErrInvalidProfile = errors.New("invalid profile")
)
