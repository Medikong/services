package bulk

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
)

type Repository interface {
	Create(context.Context, Job, reliability.Event, reliability.Command) (Job, error)
	Find(context.Context, string) (Job, error)
	FindDue(context.Context, time.Time, int) ([]Job, error)
	Lease(context.Context, string, int64, string, time.Time, time.Time) (Job, error)
	AggregateResult(context.Context, string, int64, ResultDelta, reliability.Command) (Job, error)
}
