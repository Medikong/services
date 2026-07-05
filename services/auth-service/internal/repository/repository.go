package repository

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/model"
)

var (
	ErrAlreadyExists       = errors.New("auth account already exists")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrSessionNotFound     = errors.New("session not found")
	ErrPrincipalNotAllowed = errors.New("principal not allowed")
)

type SessionInput struct {
	AuthAccountID string
	UserID        string
	Roles         []string
	AuthMethods   []string
}

type Session struct {
	SessionID    string
	AccessToken  string
	RefreshToken string
	Principal    model.AccountCredential
	AuthMethods  []string
}

type SessionRotation struct {
	PreviousAccessToken string
	Session             Session
}

type Store interface {
	CreateEmailAccount(ctx context.Context, email string, passwordHash string) (model.AccountCredential, error)
	FindByEmail(ctx context.Context, email string) (model.AccountCredential, error)
	CreateSession(ctx context.Context, input SessionInput) (Session, error)
	FindSessionByAccessToken(ctx context.Context, token string) (Session, error)
	RefreshSession(ctx context.Context, refreshToken string) (SessionRotation, error)
	RevokeSession(ctx context.Context, sessionID string) (Session, error)
	RevokeByAccessToken(ctx context.Context, token string) (Session, error)
	IssueTestToken(ctx context.Context, token string, userID string, roles []string) (Session, error)
}
