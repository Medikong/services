package repository

import (
	"context"

	"github.com/Medikong/services/services/backoffice-service/internal/model"
)

type Store interface {
	PrepareLocal(ctx context.Context, input model.PrepareDropInput) error
	MarkCouponPrepared(ctx context.Context, dropID string, policyID string) error
	Readiness(ctx context.Context, dropID string) (model.Readiness, error)
}
