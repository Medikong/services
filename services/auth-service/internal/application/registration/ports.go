package registration

import (
	"context"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

type Repository interface {
	Create(context.Context, domainregistration.Registration) error
	Find(context.Context, uuid.UUID) (domainregistration.Registration, error)
	FindForUpdate(context.Context, uuid.UUID) (domainregistration.Registration, error)
	Save(context.Context, *domainregistration.Registration) error
}

type ChallengeRepository interface {
	Issue(context.Context, domainchallenge.Challenge) error
	FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error)
	Save(context.Context, *domainchallenge.Challenge) error
	StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error
	StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error
}

type IdentityRepository interface {
	Reserve(context.Context, domainidentity.Identity) error
	FindByIDForUpdate(context.Context, uuid.UUID) (domainidentity.Identity, error)
	MarkVerified(context.Context, uuid.UUID) error
	CreatePasswordCredential(context.Context, uuid.UUID, string) error
	CreateActiveLink(context.Context, domainidentity.Link) error
	FindActiveLinkForIdentityUser(context.Context, uuid.UUID, uuid.UUID) (domainidentity.Link, error)
}

type IdempotencyRepository interface {
	FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error)
	ClaimProcessing(context.Context, domainidempotency.Record, string) (domainidempotency.Record, bool, error)
	CreateCompleted(context.Context, domainidempotency.Record, string, string) error
	Complete(context.Context, uuid.UUID, string) error
	CreateReplayPayload(context.Context, domainidempotency.ReplayPayload) error
	FindReplayPayloadForUpdate(context.Context, uuid.UUID) (domainidempotency.ReplayPayload, error)
	RecordReplay(context.Context, uuid.UUID) error
	AttachReplayPayload(context.Context, uuid.UUID, uuid.UUID) error
}

type IntentRepository interface {
	FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error)
	FindCompletionReplayForUpdate(context.Context, uuid.UUID, uuid.UUID) (domainintent.Intent, error)
	Consume(context.Context, uuid.UUID, uuid.UUID, string) error
}

type UserAuthStateRepository interface {
	CreateActiveForRegistration(context.Context, uuid.UUID, int64, string) error
}

type OutboxAppender interface {
	Append(context.Context, domainoutbox.Event) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Registrations Repository
	Challenges    ChallengeRepository
	Identities    IdentityRepository
	Idempotency   IdempotencyRepository
	Intents       IntentRepository
	UserAuthState UserAuthStateRepository
	Outbox        OutboxAppender
	Audit         AuditAppender
	Session       applicationsession.TxRepositories
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type Cryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	EqualHash([]byte, []byte) bool
	Opaque(string) (string, error)
	VerificationCode() (string, error)
	Seal(any) ([]byte, error)
	Open([]byte, any) error
	SealVirtual(any) ([]byte, error)
}

type PasswordHasher interface {
	HashPassword(string) (string, error)
}

type IntentOwnershipVerifier interface {
	VerifyOwnership(domainintent.Intent, string, string, bool) (domainintent.Intent, error)
}

type CompletionProofSigner interface {
	SignRegistrationCompletion(string, time.Duration) (string, error)
}

type UserCreationProofVerifier interface {
	VerifyUserCreation(string) (registrationID string, userID string, userVersion int64, err error)
}

type SessionIssuer interface {
	IssueTx(context.Context, applicationsession.TxRepositories, applicationsession.IssueInput) (applicationsession.Issued, error)
}

type Clock interface {
	Now() time.Time
}
