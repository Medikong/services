package recovery

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
)

type Repository interface {
	RecordFailure(context.Context, Recovery, reliability.Event, reliability.Command) (Recovery, error)
	Find(context.Context, string) (Recovery, error)
	FindDue(context.Context, time.Time, int) ([]Recovery, error)
	RequestRetry(context.Context, string, RetryRequest, reliability.Command) (Recovery, error)
	Lease(context.Context, string, int64, string, string, string, time.Time, time.Time) (Recovery, error)
	RecordResult(context.Context, string, ReplayResult, reliability.Command) (Recovery, error)
	Finalize(context.Context, string, Finalization, reliability.Command) (Recovery, error)
}
