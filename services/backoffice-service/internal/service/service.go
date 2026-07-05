package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/backoffice-service/internal/model"
	"github.com/Medikong/services/services/backoffice-service/internal/repository"
)

type Store = repository.Store

type Service struct {
	store        repository.Store
	couponClient CouponClient
}

func New(store repository.Store, couponClient CouponClient) Service {
	return Service{store: store, couponClient: couponClient}
}

func (s Service) PrepareDrop(ctx context.Context, p principal.Principal, input model.PrepareDropInput) (model.Readiness, error) {
	if !p.HasRole("operator") {
		return model.Readiness{}, ErrForbidden
	}
	if strings.TrimSpace(input.ProductID) == "" || strings.TrimSpace(input.DropID) == "" || input.StockQuantity <= 0 || input.CouponPolicy.TotalQuantity <= 0 {
		return model.Readiness{}, ErrInvalidPrepareRequest
	}
	if err := s.store.PrepareLocal(ctx, input); err != nil {
		return model.Readiness{}, err
	}
	if err := s.couponClient.PreparePolicy(ctx, input); err != nil {
		return model.Readiness{}, err
	}
	if err := s.store.MarkCouponPrepared(ctx, input.DropID, input.CouponPolicy.PolicyID); err != nil {
		return model.Readiness{}, err
	}
	return s.store.Readiness(ctx, input.DropID)
}

func (s Service) Readiness(ctx context.Context, p principal.Principal, dropID string) (model.Readiness, error) {
	if !p.HasRole("operator") {
		return model.Readiness{}, ErrForbidden
	}
	if strings.TrimSpace(dropID) == "" {
		return model.Readiness{}, ErrInvalidPrepareRequest
	}
	return s.store.Readiness(ctx, dropID)
}

var (
	ErrForbidden             = errors.New("forbidden")
	ErrInvalidPrepareRequest = errors.New("invalid prepare request")
)
