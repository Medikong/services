package session

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/principal"
)

var (
	ErrMissingBearerToken  = errors.New("missing bearer token")
	ErrMissingRefreshToken = errors.New("missing refresh token")
	ErrMissingSessionID    = errors.New("missing session id")
	ErrMissingUserID       = errors.New("missing user id")
)

type RefreshInput struct {
	RefreshToken string `json:"refreshToken"`
}

type Service struct {
	repo    Repository
	builder principal.Builder
	cache   principal.AuthzCache
}

func NewService(repo Repository, builder principal.Builder, cache principal.AuthzCache) Service {
	return Service{repo: repo, builder: builder, cache: cache}
}

func (s Service) Introspect(ctx context.Context, authorization string) (principal.AuthResult, error) {
	token := bearerToken(authorization)
	if token == "" {
		return principal.AuthResult{}, ErrMissingBearerToken
	}
	if s.cache != nil {
		if p, ok := s.cache.Get(token); ok {
			return principal.AuthResult{UserID: p.UserID, AccessToken: token, Principal: p}, nil
		}
	}
	record, err := s.repo.FindByAccessToken(ctx, token)
	if err != nil {
		return principal.AuthResult{}, err
	}
	if strings.TrimSpace(record.UserID) == "" {
		return principal.AuthResult{}, ErrMissingUserID
	}
	result, err := s.authResult(ctx, record)
	if err != nil {
		return principal.AuthResult{}, err
	}
	if s.cache != nil {
		s.cache.Set(token, result.Principal)
	}
	return result, nil
}

func (s Service) Refresh(ctx context.Context, input RefreshInput) (principal.AuthResult, error) {
	refreshToken := strings.TrimSpace(input.RefreshToken)
	if refreshToken == "" {
		return principal.AuthResult{}, ErrMissingRefreshToken
	}
	rotation, err := s.repo.Refresh(ctx, refreshToken)
	if err != nil {
		return principal.AuthResult{}, err
	}
	if s.cache != nil {
		s.cache.Delete(rotation.PreviousAccessToken)
	}
	return s.authResult(ctx, rotation.Session)
}

func (s Service) Logout(ctx context.Context, authorization string) error {
	token := bearerToken(authorization)
	if token == "" {
		return ErrMissingBearerToken
	}
	record, err := s.repo.RevokeByAccessToken(ctx, token)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(token)
		s.cache.Delete(record.AccessToken)
	}
	return nil
}

func (s Service) Revoke(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrMissingSessionID
	}
	record, err := s.repo.RevokeBySessionID(ctx, sessionID)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(record.AccessToken)
	}
	return nil
}

func (s Service) authResult(ctx context.Context, record Record) (principal.AuthResult, error) {
	p, header, err := s.builder.Build(ctx, principal.Input{
		SessionID:     record.SessionID,
		AuthAccountID: record.AuthAccountID,
		UserID:        record.UserID,
		AuthMethods:   record.AuthMethods,
	})
	if err != nil {
		return principal.AuthResult{}, err
	}
	return principal.AuthResult{
		AuthAccountID:   record.AuthAccountID,
		UserID:          record.UserID,
		AccessToken:     record.AccessToken,
		RefreshToken:    record.RefreshToken,
		Principal:       p,
		PrincipalHeader: header,
	}, nil
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}
