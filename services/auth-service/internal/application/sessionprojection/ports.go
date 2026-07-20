package sessionprojection

import (
	"context"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type Repository interface {
	Claim(context.Context, string, int, time.Duration) ([]ClaimedChange, error)
	MarkDelivered(context.Context, uuid.UUID, string) error
	ReleaseForRetry(context.Context, uuid.UUID, string, time.Duration, string) error
	DeleteDeliveredBefore(context.Context, time.Time, int) (int64, error)
}

type Sink interface {
	Apply(context.Context, domainsession.StatusChange) error
}

// ProjectionAppender is bound to the PostgreSQL transaction owned by a use case.
type ProjectionAppender interface {
	Enqueue(context.Context, []domainsession.StatusChange) error
}
