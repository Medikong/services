package couponcode

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
	Code             Code
	BatchVersion     int64
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
	Rejected         bool
	ReasonCode       string
}

type Repository interface {
	FindByHash(context.Context, []byte) (Code, error)
	Reject(context.Context, []byte, string, string, string, Command) (Mutation, error)
	Reserve(context.Context, []byte, string, string, time.Time, Command) (Mutation, error)
	Confirm(context.Context, string, string, string, int64, Command) (Mutation, error)
	Release(context.Context, string, string, int64, Command) (Mutation, error)
}
