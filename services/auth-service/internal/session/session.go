package session

import "context"

const (
	AuthMethodPassword  = "password"
	AuthMethodTestToken = "test_token"
)

type Input struct {
	AuthAccountID string
	UserID        string
	AuthMethods   []string
}

type Record struct {
	SessionID     string
	AccessToken   string
	RefreshToken  string
	AuthAccountID string
	UserID        string
	AuthMethods   []string
}

type Rotation struct {
	PreviousAccessToken string
	Session             Record
}

type Repository interface {
	Create(ctx context.Context, input Input) (Record, error)
	CreateFixedAccess(ctx context.Context, input Input, accessToken string) (Record, error)
	FindByAccessToken(ctx context.Context, token string) (Record, error)
	Refresh(ctx context.Context, refreshToken string) (Rotation, error)
	RevokeBySessionID(ctx context.Context, sessionID string) (Record, error)
	RevokeByAccessToken(ctx context.Context, token string) (Record, error)
}
