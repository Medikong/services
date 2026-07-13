package campaign

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

type Command struct {
	OperationType string
	BusinessKey   string
	RequestHash   string
	CorrelationID string
	CausationID   string
	TraceID       string
	ApprovalRef   string
	OccurredAt    time.Time
	LeaseUntil    time.Time
	ExpiresAt     time.Time
}

type Mutation struct {
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
}

type PolicyVersion struct {
	Version          int64
	EffectiveAt      time.Time
	Benefits         []Benefit
	Applicability    []ApplicabilityPolicy
	IssuerAndFunding shared.IssuerAndFunding
}

type QuantityMutation struct {
	Mutation
	Reservation QuantityReservation
	Version     int64
	Rejected    bool
	ReasonCode  string
}

type Repository interface {
	Create(context.Context, Campaign, Command) (Mutation, error)
	Get(context.Context, string) (Campaign, error)
	ConfigureIssuance(context.Context, string, int64, QuantityLimit, Command) (Mutation, error)
	Review(context.Context, string, int64, Status, string, Command) (Mutation, error)
	AddPolicyVersion(context.Context, string, int64, PolicyVersion, Command) (Mutation, error)
	ReserveQuantity(context.Context, string, string, int64, int64, time.Time, Command) (QuantityMutation, error)
	ConfirmQuantity(context.Context, string, string, int64, Command) (QuantityMutation, error)
	ReleaseQuantity(context.Context, string, string, int64, Command) (QuantityMutation, error)
}
