package account

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/crypto/bcrypt"

	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/autherror"
	"github.com/Medikong/services/services/auth-service/internal/config"
	"github.com/Medikong/services/services/auth-service/internal/credential"
	"github.com/Medikong/services/services/auth-service/internal/postgres"
	"github.com/Medikong/services/services/auth-service/internal/principal"
	"github.com/Medikong/services/services/auth-service/internal/rolegrant"
	"github.com/Medikong/services/services/auth-service/internal/session"
	"github.com/Medikong/services/services/auth-service/internal/userlink"
)

var ErrInvalidSignup = errors.New("invalid signup input")

type SignupInput struct {
	Email    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type Repositories struct {
	Accounts    Repository
	Credentials credential.Repository
	UserLinks   userlink.Repository
	RoleGrants  rolegrant.Repository
	Sessions    session.Repository
}

type RepositoryFactory func(exec postgres.Executor) Repositories

type Transactor interface {
	WithTx(ctx context.Context, fn func(postgres.Executor) error) error
}

type Service struct {
	transactor  Transactor
	repos       Repositories
	repoFactory RepositoryFactory
	builder     principal.Builder
}

func NewService(transactor Transactor, repos Repositories, repoFactory RepositoryFactory, builder principal.Builder) Service {
	return Service{
		transactor:  transactor,
		repos:       repos,
		repoFactory: repoFactory,
		builder:     builder,
	}
}

func (s Service) Signup(ctx context.Context, input SignupInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.signup", attribute.String("auth.method", session.AuthMethodPassword))
	defer span.End()

	email := normalizeEmail(input.Email)
	if email == "" || strings.TrimSpace(input.Password) == "" {
		return principal.AuthResult{}, ErrInvalidSignup
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return principal.AuthResult{}, err
	}

	var created session.Record
	err = s.transactor.WithTx(ctx, func(exec postgres.Executor) error {
		repos := s.repoFactory(exec)
		authAccountID := postgres.NewID("auth")
		userID := postgres.NewID("user")
		if err := repos.Accounts.Create(ctx, Account{AuthAccountID: authAccountID}); err != nil {
			return err
		}
		if err := repos.Credentials.CreatePassword(ctx, credential.PasswordCredential{
			AuthAccountID: authAccountID,
			Email:         email,
			PasswordHash:  string(hash),
		}); err != nil {
			return err
		}
		if err := repos.UserLinks.Create(ctx, userlink.Link{AuthAccountID: authAccountID, UserID: userID}); err != nil {
			return err
		}
		if err := repos.RoleGrants.Grant(ctx, rolegrant.Grant{AuthAccountID: authAccountID, Role: string(rbac.RoleCustomer)}); err != nil {
			return err
		}
		var err error
		created, err = repos.Sessions.Create(ctx, session.Input{
			AuthAccountID: authAccountID,
			UserID:        userID,
			AuthMethods:   []string{session.AuthMethodPassword},
		})
		return err
	})
	if err != nil {
		return principal.AuthResult{}, err
	}
	return s.authResult(ctx, created)
}

func (s Service) Login(ctx context.Context, input LoginInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.login", attribute.String("auth.method", session.AuthMethodPassword))
	defer span.End()

	credential, err := s.repos.Credentials.FindPasswordByEmail(ctx, normalizeEmail(input.Email))
	if err != nil {
		return principal.AuthResult{}, autherror.ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(credential.PasswordHash), []byte(input.Password)); err != nil {
		return principal.AuthResult{}, autherror.ErrInvalidCredentials
	}
	link, err := s.repos.UserLinks.FindByAuthAccountID(ctx, credential.AuthAccountID)
	if err != nil {
		return principal.AuthResult{}, err
	}
	record, err := s.repos.Sessions.Create(ctx, session.Input{
		AuthAccountID: credential.AuthAccountID,
		UserID:        link.UserID,
		AuthMethods:   []string{session.AuthMethodPassword},
	})
	if err != nil {
		return principal.AuthResult{}, err
	}
	return s.authResult(ctx, record)
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

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
