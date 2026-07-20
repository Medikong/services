package reauth

import (
	"context"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainreauth "github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type IdentityReader interface {
	FindActiveEmailCredentialForUser(context.Context, uuid.UUID) (domainidentity.Identity, domainidentity.PasswordCredential, error)
	FindActiveLinkForIdentity(context.Context, uuid.UUID) (domainidentity.Link, error)
}

type Repository interface {
	Create(context.Context, domainreauth.Proof) error
	FindActiveForUpdate(context.Context, []byte, uuid.UUID, uuid.UUID, string) (domainreauth.Proof, error)
	Consume(context.Context, uuid.UUID) error
}

type IdempotencyRepository interface {
	FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error)
	ClaimProcessing(context.Context, domainidempotency.Record, string) (domainidempotency.Record, bool, error)
	AttachReplayPayload(context.Context, uuid.UUID, uuid.UUID) error
	Complete(context.Context, uuid.UUID, string) error
	CreateReplayPayload(context.Context, domainidempotency.ReplayPayload) error
	FindReplayPayloadForUpdate(context.Context, uuid.UUID) (domainidempotency.ReplayPayload, error)
	RecordReplay(context.Context, uuid.UUID) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Identities  IdentityReader
	Proofs      Repository
	Sessions    applicationsession.TxRepositories
	Idempotency IdempotencyRepository
	Audit       AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type SessionRotator interface {
	RotateForDeliveryTx(context.Context, applicationsession.TxRepositories, applicationsession.RotationInput) (applicationsession.Issued, error)
}

type Cryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	Opaque(string) (string, error)
	VerifyPassword(string, string) bool
	SealOutput(Output) ([]byte, error)
	OpenOutput([]byte) (Output, error)
}

type Clock interface {
	Now() time.Time
}

type ProofConsumer interface {
	ConsumeProofID(context.Context, Repository, string, domainsession.Principal, string) (uuid.UUID, error)
}
