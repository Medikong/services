package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-authz/rbac"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/crypto/bcrypt"

	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/config"
	"github.com/Medikong/services/services/auth-service/internal/model"
	"github.com/Medikong/services/services/auth-service/internal/repository"
)

type Service struct {
	store repository.Store
	cache AuthzCache
}

type Store = repository.Store

type AuthzCache interface {
	Get(accessToken string) (principal.Principal, bool)
	Set(accessToken string, p principal.Principal)
	Delete(accessToken string)
}

type Option func(*Service)

func New(store repository.Store, options ...Option) Service {
	s := Service{store: store}
	for _, option := range options {
		option(&s)
	}
	return s
}

func WithAuthzCache(cache AuthzCache) Option {
	return func(s *Service) {
		s.cache = cache
	}
}

type SignupInput struct {
	Email    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type TestTokenInput struct {
	Token  string
	UserID string
	Roles  []string
}

type RefreshInput struct {
	RefreshToken string `json:"refreshToken"`
}

func (s Service) Signup(ctx context.Context, input SignupInput) (model.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.signup", attribute.String("auth.method", "password"))
	defer span.End()

	email := normalizeEmail(input.Email)
	if email == "" || strings.TrimSpace(input.Password) == "" {
		return model.AuthResult{}, ErrInvalidSignup
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return model.AuthResult{}, err
	}
	account, err := s.store.CreateEmailAccount(ctx, email, string(hash))
	if err != nil {
		return model.AuthResult{}, err
	}
	session, err := s.store.CreateSession(ctx, repository.SessionInput{
		AuthAccountID: account.AuthAccountID,
		UserID:        account.UserID,
		Roles:         account.Roles,
		AuthMethods:   []string{"password"},
	})
	if err != nil {
		return model.AuthResult{}, err
	}
	return buildAuthResult(session)
}

func (s Service) Login(ctx context.Context, input LoginInput) (model.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.login", attribute.String("auth.method", "password"))
	defer span.End()

	account, err := s.store.FindByEmail(ctx, normalizeEmail(input.Email))
	if err != nil {
		return model.AuthResult{}, repository.ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(input.Password)); err != nil {
		return model.AuthResult{}, repository.ErrInvalidCredentials
	}
	session, err := s.store.CreateSession(ctx, repository.SessionInput{
		AuthAccountID: account.AuthAccountID,
		UserID:        account.UserID,
		Roles:         account.Roles,
		AuthMethods:   []string{"password"},
	})
	if err != nil {
		return model.AuthResult{}, err
	}
	return buildAuthResult(session)
}

func (s Service) IssueTestToken(ctx context.Context, input TestTokenInput) (model.AuthResult, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "auth.issue_test_token")
	defer span.End()

	token := strings.TrimSpace(input.Token)
	if token == "" {
		token = "test-" + randomHex(12)
	}
	roles := input.Roles
	if len(roles) == 0 {
		roles = []string{string(rbac.RoleCustomer)}
	}
	session, err := s.store.IssueTestToken(ctx, token, strings.TrimSpace(input.UserID), roles)
	if err != nil {
		return model.AuthResult{}, err
	}
	return buildAuthResult(session)
}

func (s Service) Introspect(ctx context.Context, authorization string) (principal.Principal, error) {
	token := bearerToken(authorization)
	if token == "" {
		return principal.Principal{}, ErrMissingBearerToken
	}
	if s.cache != nil {
		if p, ok := s.cache.Get(token); ok {
			return p, nil
		}
	}
	session, err := s.store.FindSessionByAccessToken(ctx, token)
	if err != nil {
		return principal.Principal{}, err
	}
	p := principalFromSession(session)
	if strings.TrimSpace(p.UserID) == "" {
		return principal.Principal{}, ErrMissingUserID
	}
	if s.cache != nil {
		s.cache.Set(token, p)
	}
	return p, nil
}

func (s Service) Refresh(ctx context.Context, input RefreshInput) (model.AuthResult, error) {
	refreshToken := strings.TrimSpace(input.RefreshToken)
	if refreshToken == "" {
		return model.AuthResult{}, ErrMissingRefreshToken
	}
	rotation, err := s.store.RefreshSession(ctx, refreshToken)
	if err != nil {
		return model.AuthResult{}, err
	}
	if s.cache != nil {
		s.cache.Delete(rotation.PreviousAccessToken)
	}
	return buildAuthResult(rotation.Session)
}

func (s Service) Logout(ctx context.Context, authorization string) error {
	token := bearerToken(authorization)
	if token == "" {
		return ErrMissingBearerToken
	}
	session, err := s.store.RevokeByAccessToken(ctx, token)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(token)
		s.cache.Delete(session.AccessToken)
	}
	return nil
}

func (s Service) Revoke(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrMissingSessionID
	}
	session, err := s.store.RevokeSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(session.AccessToken)
	}
	return nil
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func buildAuthResult(session repository.Session) (model.AuthResult, error) {
	p := principalFromSession(session)
	header, err := principal.EncodeHeader(p)
	if err != nil {
		return model.AuthResult{}, err
	}
	return model.AuthResult{
		AuthAccountID:   session.Principal.AuthAccountID,
		UserID:          session.Principal.UserID,
		AccessToken:     session.AccessToken,
		RefreshToken:    session.RefreshToken,
		Principal:       p,
		PrincipalHeader: header,
	}, nil
}

func principalFromSession(session repository.Session) principal.Principal {
	return principal.Principal{
		Type:        principal.TypeUser,
		UserID:      session.Principal.UserID,
		Roles:       append([]string(nil), session.Principal.Roles...),
		AuthMethods: append([]string(nil), session.AuthMethods...),
		AuthLevel:   "normal",
		SessionID:   session.SessionID,
		ClientType:  "api",
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto random failed: %v", err))
	}
	return hex.EncodeToString(buf)
}

var (
	ErrInvalidSignup       = errors.New("invalid signup input")
	ErrMissingBearerToken  = errors.New("missing bearer token")
	ErrMissingRefreshToken = errors.New("missing refresh token")
	ErrMissingSessionID    = errors.New("missing session id")
	ErrMissingUserID       = errors.New("missing user id")
)
