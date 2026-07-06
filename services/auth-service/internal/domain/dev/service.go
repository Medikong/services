package dev

import (
	"context"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/domain/account"
	"github.com/Medikong/services/services/auth-service/internal/domain/principal"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userlink"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

type TestTokenInput struct {
	Token  string
	UserID string
	Email  string
	Roles  []string
}

type Service struct {
	pool        *pgxpool.Pool
	repoFactory account.RepositoryFactory
	builder     principal.Builder
	tokens      session.TokenManager
	enabled     bool
	now         func() time.Time
}

func NewService(pool *pgxpool.Pool, repoFactory account.RepositoryFactory, builder principal.Builder, tokens session.TokenManager, enabled bool) Service {
	return Service{pool: pool, repoFactory: repoFactory, builder: builder, tokens: tokens, enabled: enabled, now: time.Now}
}

func (s Service) IssueTestToken(ctx context.Context, input TestTokenInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.issue_test_token", attribute.String("auth.method", session.AuthMethodTestToken))
	defer span.End()

	if !s.enabled {
		return principal.AuthResult{}, ErrDisabled.New("dev test-token endpoint is disabled")
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		token = s.tokens.NewJTI()
	}
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		userID = "test-" + token
	}
	email := strings.TrimSpace(input.Email)
	if email == "" {
		email = userID + "@example.test"
	}
	roles := input.Roles
	if len(roles) == 0 {
		roles = []string{string(rbac.RoleCustomer)}
	}
	for index, role := range roles {
		canonical, ok := rbac.Canonical(role)
		if !ok {
			return principal.AuthResult{}, session.ErrInvalidRole.With("role", role).New("invalid test-token role")
		}
		roles[index] = string(canonical)
	}
	authAccountID := "test-auth-" + userID

	var created session.Record
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		repos := s.repoFactory(tx)
		authAccount, err := account.New(authAccountID)
		if err != nil {
			return err
		}
		if _, err := repos.Accounts.Ensure(ctx, authAccount); err != nil {
			return err
		}
		if err := repos.UserLinks.Upsert(ctx, userlink.Link{AuthAccountID: authAccountID, UserID: userID}); err != nil {
			return err
		}
		if err := repos.RoleGrants.Replace(ctx, authAccountID, roles); err != nil {
			return err
		}
		sessionRecord, err := repos.Sessions.Create(ctx, session.NewInput(s.tokens, s.now(), session.Input{
			AuthAccountID: authAccountID,
			UserID:        userID,
			Email:         email,
			AuthMethods:   []string{session.AuthMethodTestToken},
		}))
		created = sessionRecord
		return err
	})
	if err != nil {
		return principal.AuthResult{}, ErrInternal.With("operation", "issue_test_token").Wrap(err)
	}
	return s.authResult(ctx, created)
}

func (s Service) authResult(ctx context.Context, record session.Record) (principal.AuthResult, error) {
	p, header, err := s.builder.Build(ctx, principal.Input{
		SessionID:     record.SessionID,
		AuthAccountID: record.AuthAccountID,
		UserID:        record.UserID,
		AuthMethods:   record.AuthMethods,
	})
	if err != nil {
		return principal.AuthResult{}, ErrInternal.With("operation", "build_principal").Wrap(err)
	}
	role, err := session.JWTAccessRole(p.Roles)
	if err != nil {
		return principal.AuthResult{}, err
	}
	accessToken, err := s.tokens.Issue(session.AccessTokenInput{
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
