package session

import (
	"context"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/Medikong/services/services/auth-service/internal/domain/principal"
)

type RefreshInput struct {
	RefreshToken string `json:"refreshToken"`
}

type Service struct {
	repo    Repository
	builder principal.Builder
	tokens  TokenManager
	now     func() time.Time
}

func NewService(repo Repository, builder principal.Builder, tokens TokenManager) Service {
	return Service{repo: repo, builder: builder, tokens: tokens, now: time.Now}
}

func (s Service) Introspect(ctx context.Context, authorization string) (principal.AuthResult, error) {
	token, err := bearerToken(authorization)
	if err != nil {
		return principal.AuthResult{}, err
	}
	claims, err := s.tokens.Verify(token)
	if err != nil {
		return principal.AuthResult{}, err
	}
	record, err := s.repo.FindByAccessJTI(ctx, claims.ID)
	if err != nil {
		return principal.AuthResult{}, err
	}
	if strings.TrimSpace(record.UserID) == "" {
		return principal.AuthResult{}, ErrMissingUserID.New("missing user id")
	}
	if record.UserID != claims.Subject {
		return principal.AuthResult{}, ErrInvalidToken.New("JWT claims do not match session")
	}
	result, err := s.authResult(ctx, record)
	if err != nil {
		return principal.AuthResult{}, ErrInternal.With("operation", "introspect.build_principal").Wrap(err)
	}
	if !result.Principal.HasRole(claims.Role) {
		return principal.AuthResult{}, ErrInvalidToken.New("JWT role does not match principal")
	}
	result.AccessToken = token
	return result, nil
}

func (s Service) Refresh(ctx context.Context, input RefreshInput) (principal.AuthResult, error) {
	refreshToken := strings.TrimSpace(input.RefreshToken)
	if refreshToken == "" {
		return principal.AuthResult{}, ErrMissingRefreshToken.New("missing refresh token")
	}
	rotation, err := s.repo.Refresh(ctx, refreshToken, s.newInput(Input{}))
	if err != nil {
		return principal.AuthResult{}, err
	}
	return s.authResult(ctx, rotation.Session)
}

func (s Service) Logout(ctx context.Context, authorization string) error {
	token, err := bearerToken(authorization)
	if err != nil {
		return err
	}
	claims, err := s.tokens.Verify(token)
	if err != nil {
		return err
	}
	_, err = s.repo.RevokeByAccessJTI(ctx, claims.ID)
	return nil
}

func (s Service) Revoke(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrMissingSessionID.New("missing session id")
	}
	_, err := s.repo.RevokeBySessionID(ctx, sessionID)
	return err
}

func (s Service) authResult(ctx context.Context, record Record) (principal.AuthResult, error) {
	p, header, err := s.builder.Build(ctx, principal.Input{
		SessionID:     record.SessionID,
		AuthAccountID: record.AuthAccountID,
		UserID:        record.UserID,
		AuthMethods:   record.AuthMethods,
	})
	if err != nil {
		return principal.AuthResult{}, ErrInternal.With("operation", "build_principal").Wrap(err)
	}
	role, err := JWTAccessRole(p.Roles)
	if err != nil {
		return principal.AuthResult{}, err
	}
	accessToken, err := s.tokens.Issue(AccessTokenInput{
		Subject:   record.UserID,
		Role:      role,
		JTI:       record.AccessJTI,
		IssuedAt:  record.AccessExpiresAt.Add(-s.tokens.AccessTokenTTL()),
		ExpiresAt: record.AccessExpiresAt,
	})
	if err != nil {
		return principal.AuthResult{}, err
	}
	return principal.AuthResult{
		AuthAccountID:   record.AuthAccountID,
		UserID:          record.UserID,
		AccessToken:     accessToken,
		RefreshToken:    record.RefreshToken,
		Principal:       p,
		PrincipalHeader: header,
	}, nil
}

func (s Service) newInput(input Input) Input {
	now := s.now().UTC()
	input.AccessJTI = s.tokens.NewJTI()
	input.AccessExpiresAt = now.Add(s.tokens.AccessTokenTTL())
	input.RefreshExpiresAt = now.Add(s.tokens.RefreshTokenTTL())
	return input
}

func NewInput(tokens TokenManager, now time.Time, input Input) Input {
	input.AccessJTI = tokens.NewJTI()
	input.AccessExpiresAt = now.UTC().Add(tokens.AccessTokenTTL())
	input.RefreshExpiresAt = now.UTC().Add(tokens.RefreshTokenTTL())
	return input
}

func JWTAccessRole(roles []string) (string, error) {
	var role rbac.Role
	for _, candidate := range roles {
		canonical, ok := rbac.Canonical(candidate)
		if !ok {
			continue
		}
		if role != "" && role != canonical {
			return "", ErrInvalidRole.New("multiple JWT roles are not supported")
		}
		role = canonical
	}
	if role == "" {
		return "", ErrInvalidRole.New("JWT role is required")
	}
	return string(role), nil
}

func bearerToken(value string) (string, error) {
	const prefix = "Bearer "
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrMissingBearerToken.New("missing bearer token")
	}
	if !strings.HasPrefix(value, prefix) {
		return "", ErrInvalidAuthorizationHeader.New("invalid authorization header")
	}
	token := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if token == "" {
		return "", ErrMissingBearerToken.New("missing bearer token")
	}
	return token, nil
}
