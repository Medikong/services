package authentication

import (
	"context"
	"time"

	applicationintent "github.com/Medikong/services/services/auth-service/internal/application/intent"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
)

type IntentRepository interface {
	FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error)
	SetRememberMe(context.Context, uuid.UUID, bool) error
	Consume(context.Context, uuid.UUID, uuid.UUID, string) error
}

type IdentityRepository interface {
	FindEmailCredentialForUpdate(context.Context, string) (domainidentity.Identity, domainidentity.Link, domainidentity.PasswordCredential, error)
	FindActivePhoneLinkForUpdate(context.Context, string) (domainidentity.Identity, domainidentity.Link, error)
	FindActiveLinkForIdentity(context.Context, uuid.UUID) (domainidentity.Link, error)
}

type ChallengeRepository interface {
	Issue(context.Context, domainchallenge.Challenge) error
	FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error)
	Save(context.Context, *domainchallenge.Challenge) error
	StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error
	StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error
}

type OutboxAppender interface {
	Append(context.Context, domainoutbox.Event) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Intents    IntentRepository
	Identities IdentityRepository
	Challenges ChallengeRepository
	Session    applicationsession.TxRepositories
	Outbox     OutboxAppender
	Audit      AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type OwnershipVerifier interface {
	VerifyOwnership(domainintent.Intent, string, string, bool) (domainintent.Intent, error)
}

type SessionIssuer interface {
	IssueTx(context.Context, applicationsession.TxRepositories, applicationsession.IssueInput) (applicationsession.Issued, error)
}

type Cryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	VerificationCode() (string, error)
	VerifyPassword(string, string) bool
	SealDelivery(string, string) ([]byte, error)
	SealVirtualCode(string) ([]byte, error)
}

type Clock interface {
	Now() time.Time
}

var _ OwnershipVerifier = (*applicationintent.BootstrapService)(nil)
