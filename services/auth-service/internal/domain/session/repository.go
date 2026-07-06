package session

import "context"

type Repository interface {
	Create(ctx context.Context, input Input) (Record, error)
	CreateFixedAccess(ctx context.Context, input Input, accessToken string) (Record, error)
	FindByAccessToken(ctx context.Context, token string) (Record, error)
	Refresh(ctx context.Context, refreshToken string) (Rotation, error)
	RevokeBySessionID(ctx context.Context, sessionID string) (Record, error)
	RevokeByAccessToken(ctx context.Context, token string) (Record, error)
}
