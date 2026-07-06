package session

import "context"

type Repository interface {
	Create(ctx context.Context, input Input) (Record, error)
	FindByAccessJTI(ctx context.Context, jti string) (Record, error)
	Refresh(ctx context.Context, refreshToken string, input Input) (Rotation, error)
	RevokeBySessionID(ctx context.Context, sessionID string) (Record, error)
	RevokeByAccessJTI(ctx context.Context, jti string) (Record, error)
}
