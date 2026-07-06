package dev

import (
	"context"
	"strings"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/account"
	"github.com/Medikong/services/services/auth-service/internal/config"
	"github.com/Medikong/services/services/auth-service/internal/postgres"
	"github.com/Medikong/services/services/auth-service/internal/principal"
	"github.com/Medikong/services/services/auth-service/internal/session"
	"github.com/Medikong/services/services/auth-service/internal/userlink"
)

type TestTokenInput struct {
	Token  string
	UserID string
	Roles  []string
}

type Service struct {
	transactor  account.Transactor
	repoFactory account.RepositoryFactory
	builder     principal.Builder
}

func NewService(transactor account.Transactor, repoFactory account.RepositoryFactory, builder principal.Builder) Service {
	return Service{transactor: transactor, repoFactory: repoFactory, builder: builder}
}

func (s Service) IssueTestToken(ctx context.Context, input TestTokenInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.issue_test_token", attribute.String("auth.method", session.AuthMethodTestToken))
	defer span.End()

	token := strings.TrimSpace(input.Token)
	if token == "" {
		token = "test-" + postgres.RandomHex(12)
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
	err := s.transactor.WithTx(ctx, func(exec postgres.Executor) error {
		repos := s.repoFactory(exec)
		if err := repos.Accounts.Create(ctx, account.Account{AuthAccountID: authAccountID}); err != nil {
			return err
		}
		if err := repos.UserLinks.Upsert(ctx, userlink.Link{AuthAccountID: authAccountID, UserID: userID}); err != nil {
			return err
		}
		if err := repos.RoleGrants.Replace(ctx, authAccountID, roles); err != nil {
			return err
		}
		if err := revokeFixedToken(ctx, exec, token); err != nil {
			return err
		}
		var err error
		created, err = repos.Sessions.CreateFixedAccess(ctx, session.Input{
			AuthAccountID: authAccountID,
			UserID:        userID,
			AuthMethods:   []string{session.AuthMethodTestToken},
		}, token)
		return err
	})
	if err != nil {
		return principal.AuthResult{}, err
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

func revokeFixedToken(ctx context.Context, exec postgres.Executor, accessToken string) error {
	_, err := exec.ExecContext(ctx, `DELETE FROM auth_sessions WHERE access_token = $1`, accessToken)
	return err
}
