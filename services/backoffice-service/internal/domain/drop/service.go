package drop

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-authz/principal"
)

type Service struct {
	store        Repository
	couponClient CouponClient
}

func NewService(store Repository, couponClient CouponClient) Service {
	return Service{store: store, couponClient: couponClient}
}

func (s Service) PrepareDrop(ctx context.Context, p principal.Principal, input PrepareDropInput) (Readiness, error) {
	if !p.HasRole("operator") {
		return Readiness{}, ErrForbidden
	}
	if strings.TrimSpace(input.ProductID) == "" || strings.TrimSpace(input.DropID) == "" || input.StockQuantity <= 0 || input.CouponPolicy.TotalQuantity <= 0 {
		return Readiness{}, ErrInvalidPrepareRequest
	}
	if err := s.store.PrepareLocal(ctx, input); err != nil {
		return Readiness{}, err
	}
	if err := s.couponClient.PreparePolicy(ctx, input); err != nil {
		return Readiness{}, err
	}
	if err := s.store.MarkCouponPrepared(ctx, input.DropID, input.CouponPolicy.PolicyID); err != nil {
		return Readiness{}, err
	}
	return s.store.Readiness(ctx, input.DropID)
}

func (s Service) Readiness(ctx context.Context, p principal.Principal, dropID string) (Readiness, error) {
	if !p.HasRole("operator") {
		return Readiness{}, ErrForbidden
	}
	if strings.TrimSpace(dropID) == "" {
		return Readiness{}, ErrInvalidPrepareRequest
	}
	return s.store.Readiness(ctx, dropID)
}

var (
	ErrForbidden             = errors.New("forbidden")
	ErrInvalidPrepareRequest = errors.New("invalid prepare request")
)
