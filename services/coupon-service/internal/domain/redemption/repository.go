package redemption

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

type Repository interface {
	Find(context.Context, string) (Redemption, error)
	FindConsumingByUserCoupon(context.Context, string) (Redemption, bool, error)
	Evaluate(context.Context, Evaluation, reliability.Command) (Redemption, error)
	Reserve(context.Context, string, int64, time.Time, reliability.Command) (Redemption, error)
	Confirm(context.Context, string, int64, shared.ExternalRef, any, string, reliability.Command) (Redemption, error)
	Release(context.Context, string, int64, shared.ExternalRef, any, string, reliability.Command) (Redemption, error)
	Reclaim(context.Context, string, int64, shared.ExternalRef, any, string, reliability.Command) (Redemption, error)
	Replay(context.Context, ReplayRequest, reliability.Command) (ReplayOutcome, error)
}

type ReplayOperation string

const (
	ReplayReserve ReplayOperation = "reserve"
	ReplayConfirm ReplayOperation = "confirm"
	ReplayRelease ReplayOperation = "release"
	ReplayReclaim ReplayOperation = "reclaim"
)

type ReplayResultKind string

const (
	ReplayTransitioned   ReplayResultKind = "transitioned"
	ReplayAlreadyApplied ReplayResultKind = "already_applied"
	ReplayFailed         ReplayResultKind = "failed"
)

type ReplayRequest struct {
	RecoveryID      string
	AttemptID       string
	BusinessKey     string
	RedemptionID    string
	Operation       ReplayOperation
	ExpectedVersion int64
	ReservedUntil   *time.Time
	ResultRef       *shared.ExternalRef
	ResultSnapshot  any
	ReasonCode      string
	ReplayedAt      time.Time
}

type ReplayOutcome struct {
	Redemption  Redemption       `json:"redemption"`
	ResultKind  ReplayResultKind `json:"resultKind"`
	ResultRef   string           `json:"resultRef,omitempty"`
	FailureCode string           `json:"failureCode,omitempty"`
}
