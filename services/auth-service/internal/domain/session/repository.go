package session

import (
	"context"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	Create(context.Context, pgx.Tx, CreateParams) error
	FindByWebSecret(context.Context, []byte) (Session, Credential, error)
	FindByWebSecretForUpdate(context.Context, pgx.Tx, []byte) (Session, Credential, error)
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
	FenceRevocation(context.Context, pgx.Tx, uuid.UUID) (domain.RevocationFences, error)
	FenceRevocationsForUser(context.Context, pgx.Tx, uuid.UUID) (domain.RevocationFences, error)
	FenceRevocationsForIdentityLinkExcept(context.Context, pgx.Tx, IdentityLinkRevocationScope) (domain.RevocationFences, error)
	ProjectRevoked(context.Context, uuid.UUID) error
	ProjectRevokedForUser(context.Context, uuid.UUID) error
	ProjectRevokedForIdentityLinkExcept(context.Context, uuid.UUID, uuid.UUID) error
}

type IdentityLinkRevocationScope struct {
	IdentityLinkID uuid.UUID
	KeepSessionID  uuid.UUID
}
