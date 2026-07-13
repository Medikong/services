package issuerequest

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
	Request          Request
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
}

type Admission struct {
	PerUserLimit int64
}

type Repository interface {
	Create(context.Context, Request, Admission, Command) (Mutation, error)
	Get(context.Context, string) (Request, error)
	FindDue(context.Context, time.Time, int) ([]Request, error)
	MarkPending(context.Context, string, int64, Command) (Mutation, error)
	MarkProcessing(context.Context, string, int64, Command) (Mutation, error)
	RecordFailure(context.Context, string, int64, string, bool, *time.Time, Command) (Mutation, error)
	Retry(context.Context, string, int64, time.Time, Command) (Mutation, error)
	Reject(context.Context, string, int64, string, Command) (Mutation, error)
	Complete(context.Context, string, int64, string, Command) (Mutation, error)
	FinalizeFailure(context.Context, string, int64, string, Command) (Mutation, error)
}
