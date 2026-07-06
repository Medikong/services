package drop

import (
	"context"
)

type Repository interface {
	PrepareLocal(ctx context.Context, input PrepareDropInput) error
	MarkCouponPrepared(ctx context.Context, dropID string, policyID string) error
	Readiness(ctx context.Context, dropID string) (Readiness, error)
}
