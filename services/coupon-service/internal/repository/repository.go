package repository

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/coupon-service/internal/model"
)

var (
	ErrPolicyNotFound = errors.New("coupon policy not found")
	ErrPolicyNotReady = errors.New("coupon policy not ready")
	ErrSoldOut        = errors.New("coupon sold out")
	ErrDuplicate      = errors.New("coupon duplicate")
)

type PolicyInput struct {
	PolicyID      string
	DropID        string
	Name          string
	TotalQuantity int
	Status        string
}

type Store interface {
	UpsertPolicy(ctx context.Context, input PolicyInput) (model.Policy, error)
	GetPolicy(ctx context.Context, policyID string) (model.Policy, error)
	Issue(ctx context.Context, policyID string, userID string, idempotencyKey string) (model.IssueResult, error)
	ListByUser(ctx context.Context, userID string) ([]model.Coupon, error)
}
