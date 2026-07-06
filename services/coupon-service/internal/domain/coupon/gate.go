package coupon

import "context"

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
	Coupon         Coupon
}

type Gate interface {
	PreparePolicy(ctx context.Context, policy Policy) error
	Admit(ctx context.Context, request IssueRequest) (Decision, error)
	Complete(ctx context.Context, decision Decision, result IssueResult) error
	Compensate(ctx context.Context, decision Decision) error
}
