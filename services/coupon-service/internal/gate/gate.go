package gate

import (
	"context"

	"github.com/Medikong/services/services/coupon-service/internal/model"
)

const (
	ResultIssuedCandidate = "issued_candidate"
	ResultDuplicate       = "duplicate"
	ResultSoldOut         = "sold_out"
	ResultNotReady        = "not_ready"
)

type IssueRequest struct {
	PolicyID       string
	UserID         string
	IdempotencyKey string
}

type Decision struct {
	Result         string
	PolicyID       string
	UserID         string
	IdempotencyKey string
	Coupon         model.Coupon
}

type Gate interface {
	PreparePolicy(ctx context.Context, policy model.Policy) error
	Admit(ctx context.Context, request IssueRequest) (Decision, error)
	Complete(ctx context.Context, decision Decision, result model.IssueResult) error
	Compensate(ctx context.Context, decision Decision) error
}
