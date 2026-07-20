package session

import (
	"context"
	"time"

	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
)

// Repository is the transaction-scoped persistence role used by Session use cases.
type Repository interface {
	Create(context.Context, domainsession.CreateParams) error
	FindByWebSecretForUpdate(context.Context, []byte) (domainsession.Session, domainsession.Credential, error)
	FindByRefreshSecretForUpdate(context.Context, []byte) (domainsession.Session, domainsession.Credential, error)
	FindRecoveryWebSecretForUpdate(context.Context, []byte) (domainsession.Session, domainsession.Credential, error)
	FindActiveForUpdate(context.Context, uuid.UUID) (domainsession.Session, error)
	FindActiveCredentialForUpdate(context.Context, uuid.UUID, string) (domainsession.Credential, error)
	RotateRefresh(context.Context, domainsession.Credential, domainsession.Credential) error
	RotateForDelivery(context.Context, domainsession.Credential, domainsession.Credential, time.Time) error
	Rebind(context.Context, domainsession.Session) error
	Revoke(context.Context, uuid.UUID, string) error
	RevokeForUser(context.Context, uuid.UUID, string) error
	RevokeForIdentityLinkExcept(context.Context, uuid.UUID, uuid.UUID, string) error
	MarkReuseDetected(context.Context, uuid.UUID, uuid.UUID) error
}

type UserAuthStateReader interface {
	FindForUpdate(context.Context, uuid.UUID) (domainuserauthstate.State, error)
}

type IdempotencyRepository interface {
	FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error)
	ClaimProcessing(context.Context, domainidempotency.Record, string) (domainidempotency.Record, bool, error)
	CreateCompleted(context.Context, domainidempotency.Record, string, string) error
	Complete(context.Context, uuid.UUID, string) error
	CreateReplayPayload(context.Context, domainidempotency.ReplayPayload) error
	FindReplayPayloadForUpdate(context.Context, uuid.UUID) (domainidempotency.ReplayPayload, error)
	RecordReplay(context.Context, uuid.UUID) error
	DestroyReplayPayload(context.Context, uuid.UUID) error
}

type OutboxAppender interface {
	Append(context.Context, domainoutbox.Event) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

// TxRepositories is built by infrastructure after opening a real transaction.
type TxRepositories struct {
	Sessions      Repository
	UserAuthState UserAuthStateReader
	Idempotency   IdempotencyRepository
	Outbox        OutboxAppender
	Audit         AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type AccessClaims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	TokenID   string
}

type Cryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	Opaque(string) (string, error)
	SealTokenSet(TokenSet) ([]byte, error)
	OpenTokenSet([]byte) (TokenSet, error)
	SignAccessToken(uuid.UUID, uuid.UUID, time.Duration) (string, time.Time, error)
	VerifyAccessToken(string) (AccessClaims, error)
}

type Clock interface {
	Now() time.Time
}

type StatusProjectionWriter interface {
	RevokeSession(context.Context, uuid.UUID) error
	RevokeUser(context.Context, uuid.UUID) error
}

type SessionRevocationFencer interface {
	Fence(context.Context, []domainsession.Session) (domainsession.RevocationFence, error)
}
