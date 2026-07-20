package passwordreset

import (
	"context"
	"time"

	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/google/uuid"
)

type Repository interface {
	Create(context.Context, domainpasswordreset.Reset) error
	FindForUpdate(context.Context, uuid.UUID) (domainpasswordreset.Reset, error)
	Save(context.Context, *domainpasswordreset.Reset) error
}

type ChallengeRepository interface {
	Issue(context.Context, domainchallenge.Challenge) error
	FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error)
	Save(context.Context, *domainchallenge.Challenge) error
	StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error
	StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error
}

type IdentityRepository interface {
	FindByValueForUpdate(context.Context, domainidentity.Type, string) (domainidentity.Identity, error)
	FindByIDForUpdate(context.Context, uuid.UUID) (domainidentity.Identity, error)
	ReplacePasswordCredential(context.Context, uuid.UUID, string) error
	FindActiveLinkForIdentity(context.Context, uuid.UUID) (domainidentity.Link, error)
}

type IntentRepository interface {
	FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error)
}

type IdempotencyRepository interface {
	FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error)
	CreateCompleted(context.Context, domainidempotency.Record, string, string) error
}

type SessionRevoker interface {
	RevokeForUser(context.Context, uuid.UUID, string) error
}

type OutboxAppender interface {
	Append(context.Context, domainoutbox.Event) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Resets      Repository
	Challenges  ChallengeRepository
	Identities  IdentityRepository
	Intents     IntentRepository
	Idempotency IdempotencyRepository
	Sessions    SessionRevoker
	Outbox      OutboxAppender
	Audit       AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type Cryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	Opaque(string) (string, error)
	VerificationCode() (string, error)
	Seal(any) ([]byte, error)
	SealVirtual(any) ([]byte, error)
	HashPassword(string) (string, error)
}

type IntentOwnershipVerifier interface {
	VerifyOwnership(domainintent.Intent, string, string, bool) (domainintent.Intent, error)
}

type StatusProjectionWriter interface {
	RevokeUser(context.Context, uuid.UUID) error
}

type Clock interface {
	Now() time.Time
}
