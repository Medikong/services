package usercoupon

import (
	"context"
	"encoding/json"
	"time"
)

type Command struct {
	OperationType string
	BusinessKey   string
	RequestHash   string
	CorrelationID string
	CausationID   string
	TraceID       string
	OccurredAt    time.Time
	LeaseUntil    time.Time
	ExpiresAt     time.Time
}

type Mutation struct {
	Coupon           Coupon
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
}

type Repository interface {
	Grant(context.Context, Coupon, Command) (Mutation, error)
	Get(context.Context, string) (Coupon, error)
	GetByIssueRequest(context.Context, string) (Coupon, error)
	FindExpirable(context.Context, time.Time, int) ([]Coupon, error)
	Expire(context.Context, string, int64, time.Time, Command) (Mutation, error)
}
