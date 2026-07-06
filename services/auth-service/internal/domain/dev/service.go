package dev

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

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
	Roles  []string
}

type Service struct {
	pool        *pgxpool.Pool
	repoFactory account.RepositoryFactory
	builder     principal.Builder
}

func NewService(pool *pgxpool.Pool, repoFactory account.RepositoryFactory, builder principal.Builder) Service {
	return Service{pool: pool, repoFactory: repoFactory, builder: builder}
}

func (s Service) IssueTestToken(ctx context.Context, input TestTokenInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.issue_test_token", attribute.String("auth.method", session.AuthMethodTestToken))
	defer span.End()

	token := strings.TrimSpace(input.Token)
	if token == "" {
		token = "test-" + randomHex(12)
	}
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		userID = "test-" + token
	}
	roles := input.Roles
	if len(roles) == 0 {
		roles = []string{string(rbac.RoleCustomer)}
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
		if err := revokeFixedToken(ctx, tx, token); err != nil {
			return err
		}
		sessionRecord, err := repos.Sessions.CreateFixedAccess(ctx, session.Input{
			AuthAccountID: authAccountID,
			UserID:        userID,
			AuthMethods:   []string{session.AuthMethodTestToken},
		}, token)
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
	return principal.AuthResult{
		AuthAccountID:   record.AuthAccountID,
		UserID:          record.UserID,
		AccessToken:     record.AccessToken,
		RefreshToken:    record.RefreshToken,
		Principal:       p,
		PrincipalHeader: header,
	}, nil
}

func revokeFixedToken(ctx context.Context, tx pgx.Tx, accessToken string) error {
	_, err := tx.Exec(ctx, `DELETE FROM auth_sessions WHERE access_token = $1`, accessToken)
	return err
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto random failed: %v", err))
	}
	return hex.EncodeToString(buf)
}
