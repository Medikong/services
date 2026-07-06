package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/crypto/bcrypt"

	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/domain/passwordauth"
	"github.com/Medikong/services/services/auth-service/internal/domain/principal"
	"github.com/Medikong/services/services/auth-service/internal/domain/providerlink"
	"github.com/Medikong/services/services/auth-service/internal/domain/rolegrant"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userlink"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

type SignupInput struct {
	Email    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type Repositories struct {
	Accounts      Repository
	PasswordAuth  passwordauth.Repository
	ProviderLinks providerlink.Repository
	UserLinks     userlink.Repository
	RoleGrants    rolegrant.Repository
	Sessions      session.Repository
}

type RepositoryFactory func(pgx.Tx) Repositories

type Service struct {
	pool        *pgxpool.Pool
	repos       Repositories
	repoFactory RepositoryFactory
	builder     principal.Builder
	tokens      session.TokenManager
	now         func() time.Time
}

func NewService(pool *pgxpool.Pool, repos Repositories, repoFactory RepositoryFactory, builder principal.Builder, tokens session.TokenManager) Service {
	return Service{
		pool:        pool,
		repos:       repos,
		repoFactory: repoFactory,
		builder:     builder,
		tokens:      tokens,
		now:         time.Now,
	}
}

func (s Service) Signup(ctx context.Context, input SignupInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.signup", attribute.String("auth.method", session.AuthMethodPassword))
	defer span.End()

	email := normalizeEmail(input.Email)
	if email == "" || !validPassword(input.Password) {
		return principal.AuthResult{}, ErrInvalidSignup.New("invalid signup input")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return principal.AuthResult{}, ErrInternal.With("operation", "signup.hash_password").Wrap(err)
	}

	var created session.Record
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		repos := s.repoFactory(tx)
		authAccountID := newID("auth")
		userID := newID("user")
		authAccount, err := New(authAccountID)
		if err != nil {
			return err
		}
		if _, err := repos.Accounts.Create(ctx, authAccount); err != nil {
			return err
		}
		if err := repos.PasswordAuth.CreatePassword(ctx, passwordauth.PasswordCredential{
			AuthAccountID: authAccountID,
			Email:         email,
			PasswordHash:  string(hash),
		}); err != nil {
			if errors.Is(err, passwordauth.ErrAlreadyExists) {
				return ErrEmailAlreadyExists.Wrap(err)
			}
			return err
		}
		if err := repos.UserLinks.Create(ctx, userlink.Link{AuthAccountID: authAccountID, UserID: userID}); err != nil {
			return err
		}
		if err := repos.RoleGrants.Grant(ctx, rolegrant.Grant{AuthAccountID: authAccountID, Role: string(rbac.RoleCustomer)}); err != nil {
			return err
		}
		sessionRecord, err := repos.Sessions.Create(ctx, s.newSessionInput(session.Input{
			AuthAccountID: authAccountID,
			UserID:        userID,
			Email:         email,
			AuthMethods:   []string{session.AuthMethodPassword},
		}))
		created = sessionRecord
		return err
	})
	if err != nil {
		if _, ok := oops.AsOops(err); ok {
			return principal.AuthResult{}, err
		}
		return principal.AuthResult{}, ErrInternal.With("operation", "signup").Wrap(err)
	}
	return s.authResult(ctx, created)
}

func (s Service) Login(ctx context.Context, input LoginInput) (principal.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.login", attribute.String("auth.method", session.AuthMethodPassword))
	defer span.End()

	password, err := s.repos.PasswordAuth.FindPasswordByEmail(ctx, normalizeEmail(input.Email))
	if err != nil {
		return principal.AuthResult{}, ErrInvalidCredentials.Wrap(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(password.PasswordHash), []byte(input.Password)); err != nil {
		return principal.AuthResult{}, ErrInvalidCredentials.Wrap(err)
	}
	authAccount, err := s.repos.Accounts.FindByID(ctx, password.AuthAccountID)
	if err != nil {
		return principal.AuthResult{}, ErrInvalidCredentials.Wrap(err)
	}
	if authAccount.Status != StatusActive {
		return principal.AuthResult{}, ErrInvalidCredentials.New("auth account is disabled")
	}
	link, err := s.repos.UserLinks.FindByAuthAccountID(ctx, password.AuthAccountID)
	if err != nil {
		return principal.AuthResult{}, ErrInvalidCredentials.Wrap(err)
	}
	record, err := s.repos.Sessions.Create(ctx, s.newSessionInput(session.Input{
		AuthAccountID: password.AuthAccountID,
		UserID:        link.UserID,
		Email:         password.Email,
		AuthMethods:   []string{session.AuthMethodPassword},
	}))
	if err != nil {
		return principal.AuthResult{}, ErrInternal.With("operation", "login.create_session").Wrap(err)
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

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s Service) newSessionInput(input session.Input) session.Input {
	return session.NewInput(s.tokens, s.now(), input)
}

func validPassword(password string) bool {
	if len(password) < 8 || strings.TrimSpace(password) != password {
		return false
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		if unicode.IsSpace(r) {
			return false
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, randomHex(12))
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto random failed: %v", err))
	}
	return hex.EncodeToString(buf)
}
