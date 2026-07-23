package development

import (
	"context"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

type Repository interface {
	FindChallenge(context.Context, uuid.UUID) (domainchallenge.Challenge, error)
	FindVirtualProjection(context.Context, uuid.UUID, time.Time) (domainchallenge.VirtualProjection, error)
	FindRegistrationIntent(context.Context, uuid.UUID) (uuid.UUID, error)
	FindPasswordResetIntent(context.Context, uuid.UUID) (uuid.UUID, error)
	FindRequestedLinkUser(context.Context, uuid.UUID) (uuid.UUID, error)
	FindIntentForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error)
}

type PrincipalInput struct {
	UserID     uuid.UUID
	IdentityID uuid.UUID
	LinkID     uuid.UUID
	Email      string
	ChangeID   string
}

type FixtureRepository interface {
	CreatePrincipalsBulk(context.Context, []PrincipalInput) error
	SessionBulkRepositories() applicationsession.BulkTxRepositories
}

type TxRepositories struct {
	Virtual  Repository
	Fixtures FixtureRepository
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type Cryptography interface {
	OpenVirtual([]byte, any) error
}

type IntentOwnershipVerifier interface {
	VerifyOwnership(domainintent.Intent, string, string, bool) (domainintent.Intent, error)
}

type Clock interface {
	Now() time.Time
}

type SessionIssuer interface {
	IssueBulkTx(context.Context, applicationsession.BulkTxRepositories, []applicationsession.IssueInput) ([]applicationsession.Issued, error)
}
