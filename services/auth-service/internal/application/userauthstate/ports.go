package userauthstate

import (
	"context"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
)

type StateRepository interface {
	FindForUpdate(context.Context, uuid.UUID) (domainuserauthstate.State, error)
	Apply(context.Context, domainuserauthstate.State, domainuserauthstate.Change) (domainuserauthstate.State, error)
}

type SessionRevoker interface {
	FindActiveForUserForUpdate(context.Context, uuid.UUID) ([]domainsession.Session, error)
	RevokeForUser(context.Context, uuid.UUID, string) error
}

type TxRepositories struct {
	States   StateRepository
	Sessions SessionRevoker
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type StatusProof struct {
	StatusChangeID string
	UserID         string
	AccountStatus  string
	UserVersion    int64
	ChangedAt      int64
}

type ProofVerifier interface {
	VerifyUserStatus(string) (StatusProof, error)
}

type AuthorizationDecisionPort interface {
	Verify(context.Context, string, string, string, string) error
}

type StatusProjectionWriter interface {
	RevokeUser(context.Context, uuid.UUID) error
}

type SessionRevocationFencer interface {
	Fence(context.Context, []domainsession.Session) (domainsession.RevocationFence, error)
}

type Clock interface {
	Now() time.Time
}
