package coupon

import (
	"context"
	"errors"
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

type Repository interface {
	UpsertPolicy(ctx context.Context, input PolicyInput) (Policy, error)
	GetPolicy(ctx context.Context, policyID string) (Policy, error)
	Issue(ctx context.Context, policyID string, userID string, idempotencyKey string) (IssueResult, error)
	ListByUser(ctx context.Context, userID string) ([]Coupon, error)
}
