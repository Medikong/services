package session

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	Create(context.Context, pgx.Tx, CreateParams) error
	FindByWebSecret(context.Context, []byte) (Session, Credential, error)
	FindByRefreshSecretForUpdate(context.Context, pgx.Tx, []byte) (Session, Credential, error)
	FindRecoveryWebSecretForUpdate(context.Context, pgx.Tx, []byte) (Session, Credential, error)
	FindActiveForUpdate(context.Context, pgx.Tx, uuid.UUID) (Session, error)
	FindActiveCredentialForUpdate(context.Context, pgx.Tx, uuid.UUID, string) (Credential, error)
	RotateRefresh(context.Context, pgx.Tx, Credential, Credential) error
	RotateForDelivery(context.Context, pgx.Tx, Credential, Credential, time.Time) error
	Rebind(context.Context, pgx.Tx, Session) error
	Revoke(context.Context, pgx.Tx, uuid.UUID, string) error
	RevokeForUser(context.Context, pgx.Tx, uuid.UUID, string) error
	RevokeForIdentityLinkExcept(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error
	MarkReuseDetected(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
	FindActive(context.Context, uuid.UUID) (Session, error)
}
