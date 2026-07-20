package identity

import (
	"context"
	"time"

	applicationreauth "github.com/Medikong/services/services/auth-service/internal/application/reauth"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type Repository interface {
	Reserve(context.Context, domainidentity.Identity) error
	FindByValueForUpdate(context.Context, domainidentity.Type, string) (domainidentity.Identity, error)
	FindByIDForUpdate(context.Context, uuid.UUID) (domainidentity.Identity, error)
	MarkVerified(context.Context, uuid.UUID) error
	CreatePasswordCredential(context.Context, uuid.UUID, string) error
	ReplacePasswordCredential(context.Context, uuid.UUID, string) error
	FindEmailCredentialForUpdate(context.Context, string) (domainidentity.Identity, domainidentity.Link, domainidentity.PasswordCredential, error)
	FindActivePhoneLinkForUpdate(context.Context, string) (domainidentity.Identity, domainidentity.Link, error)
	CreateActiveLink(context.Context, domainidentity.Link) error
	CreateRequestedLink(context.Context, domainidentity.Link) error
	CreatePhoneReplacementRequested(context.Context, domainidentity.Link, uuid.UUID, uuid.UUID) error
	AttachProofChallenge(context.Context, uuid.UUID, uuid.UUID) error
	ActivateLink(context.Context, uuid.UUID) error
	ReplacePhoneLink(context.Context, uuid.UUID, uuid.UUID) error
	RevokeLinksForUser(context.Context, uuid.UUID, domainidentity.Type, string) error
	RequestedLinkForUpdate(context.Context, uuid.UUID) (domainidentity.Link, domainidentity.Identity, error)
	FindActiveLinkForIdentityUser(context.Context, uuid.UUID, uuid.UUID) (domainidentity.Link, error)
	FindActiveLinkForIdentity(context.Context, uuid.UUID) (domainidentity.Link, error)
	FindActiveLinkForUserType(context.Context, uuid.UUID, domainidentity.Type) (domainidentity.Link, domainidentity.Identity, error)
	FindActiveEmailCredentialForUser(context.Context, uuid.UUID) (domainidentity.Identity, domainidentity.PasswordCredential, error)
}

type ChallengeRepository interface {
	Issue(context.Context, domainchallenge.Challenge) error
	FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error)
	Save(context.Context, *domainchallenge.Challenge) error
	StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error
	StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error
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

type OutboxAppender interface {
	Append(context.Context, domainoutbox.Event) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Identities  Repository
	Challenges  ChallengeRepository
	Proofs      applicationreauth.Repository
	Sessions    applicationsession.TxRepositories
	Revocations SessionRevoker
	Idempotency IdempotencyRepository
	Outbox      OutboxAppender
	Audit       AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type ReauthenticationProofConsumer interface {
	ConsumeProofID(context.Context, applicationreauth.Repository, string, domainsession.Principal, string) (uuid.UUID, error)
}

type SessionRotator interface {
	RotateForDeliveryTx(context.Context, applicationsession.TxRepositories, applicationsession.RotationInput) (applicationsession.Issued, error)
}

type SessionRevoker interface {
	FindActiveForIdentityLinkExceptForUpdate(context.Context, uuid.UUID, uuid.UUID) ([]domainsession.Session, error)
	RevokeForIdentityLinkExcept(context.Context, uuid.UUID, uuid.UUID, string) error
}

type SessionRevocationFencer interface {
	Fence(context.Context, []domainsession.Session) (domainsession.RevocationFence, error)
}

// SessionAuthenticator lets the HTTP adapter authenticate without importing a domain package.
type SessionAuthenticator interface {
	Authenticate(context.Context, string, string) (domainsession.Principal, error)
}

type Cryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	VerificationCode() (string, error)
	SealDelivery(string, string) ([]byte, error)
	SealVirtualCode(string) ([]byte, error)
	SealStartOutput(StartLinkOutput) ([]byte, error)
	OpenStartOutput([]byte) (StartLinkOutput, error)
	SealCompleteOutput(CompleteLinkOutput) ([]byte, error)
	OpenCompleteOutput([]byte) (CompleteLinkOutput, error)
}

type Clock interface {
	Now() time.Time
}
