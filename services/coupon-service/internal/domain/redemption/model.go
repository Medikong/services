package redemption

import (
	"math/big"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type Status string

const (
	StatusEvaluated Status = "evaluated"
	StatusRejected  Status = "rejected"
	StatusReserved  Status = "reserved"
	StatusConfirmed Status = "confirmed"
	StatusReleased  Status = "released"
	StatusReclaimed Status = "reclaimed"
)

type CostShare struct {
	BearerType string              `json:"bearerType"`
	BearerRef  *shared.ExternalRef `json:"bearerRef,omitempty"`
	Amount     shared.Money        `json:"amount"`
}

type Redemption struct {
	ID                string             `json:"redemptionId"`
	UserCouponID      string             `json:"userCouponId"`
	CampaignID        string             `json:"campaignId"`
	UserID            string             `json:"userId"`
	OrderID           string             `json:"orderId"`
	OperationType     string             `json:"operationType"`
	BusinessKey       string             `json:"businessKey"`
	Status            Status             `json:"status"`
	ReasonCode        string             `json:"reasonCode,omitempty"`
	PolicyVersion     int                `json:"policyVersion"`
	OrderSnapshot     any                `json:"orderSnapshot"`
	OrderSnapshotHash string             `json:"orderSnapshotHash"`
	EvaluatedAt       time.Time          `json:"evaluatedAt"`
	Discount          shared.Money       `json:"discount"`
	FinalOrderAmount  shared.Money       `json:"finalOrderAmount"`
	CostShares        []CostShare        `json:"costShares,omitempty"`
	ReservedUntil     *time.Time         `json:"reservedUntil,omitempty"`
	ConfirmedAt       *time.Time         `json:"confirmedAt,omitempty"`
	ReleasedAt        *time.Time         `json:"releasedAt,omitempty"`
	ReclaimedAt       *time.Time         `json:"reclaimedAt,omitempty"`
	ResultRef         shared.ExternalRef `json:"resultRef"`
	ResultSnapshot    any                `json:"resultSnapshot,omitempty"`
	Version           int64              `json:"version"`
}

type Evaluation struct {
	RedemptionID      string
	UserCouponID      string
	CampaignID        string
	UserID            string
	OrderID           string
	BusinessKey       string
	Eligible          bool
	ReasonCode        string
	PolicyVersion     int
	OrderSnapshot     any
	OrderSnapshotHash string
	Discount          shared.Money
	FinalOrderAmount  shared.Money
	CostShares        []CostShare
	EvaluatedAt       time.Time
}

func (r ReplayRequest) Validate() error {
	if !strings.HasPrefix(r.RecoveryID, "rcvy_") || !strings.HasPrefix(r.AttemptID, "att_") ||
		strings.TrimSpace(r.BusinessKey) == "" || !strings.HasPrefix(r.RedemptionID, "redm_") ||
		r.ExpectedVersion < 0 || r.ReplayedAt.IsZero() {
		return oops.In("coupon_redemption").Code("coupon.redemption_replay_invalid").New("coupon redemption replay identity, version, and time are required")
	}
	switch r.Operation {
	case ReplayReserve:
		if r.ReservedUntil == nil || r.ReservedUntil.IsZero() {
			return oops.In("coupon_redemption").Code("coupon.redemption_replay_invalid").New("reserve replay requires the original reservation deadline")
		}
	case ReplayConfirm, ReplayRelease, ReplayReclaim:
		if r.ResultRef == nil || strings.TrimSpace(r.ReasonCode) == "" {
			return oops.In("coupon_redemption").Code("coupon.redemption_replay_invalid").New("transition replay requires the original result reference and reason")
		}
		if err := r.ResultRef.Validate(); err != nil {
			return err
		}
	default:
		return oops.In("coupon_redemption").Code("coupon.redemption_replay_operation_invalid").New("coupon redemption replay operation is not supported")
	}
	return nil
}

func NewEvaluation(input Evaluation) (Redemption, reliability.Event, error) {
	if strings.TrimSpace(input.RedemptionID) == "" || strings.TrimSpace(input.UserCouponID) == "" ||
		strings.TrimSpace(input.CampaignID) == "" || strings.TrimSpace(input.UserID) == "" ||
		strings.TrimSpace(input.OrderID) == "" || strings.TrimSpace(input.BusinessKey) == "" ||
		input.PolicyVersion < 1 || input.EvaluatedAt.IsZero() || strings.TrimSpace(input.OrderSnapshotHash) == "" {
		return Redemption{}, reliability.Event{}, oops.In("coupon_redemption").Code("coupon.redemption_input_invalid").New("redemption evaluation input is incomplete")
	}
	if err := validateAmounts(input.Discount, input.FinalOrderAmount, input.CostShares); err != nil {
		return Redemption{}, reliability.Event{}, err
	}
	status := StatusEvaluated
	eventID := "EVT.A.19-19"
	eventType := "coupon.redemption.eligible"
	if !input.Eligible {
		status = StatusRejected
		eventID = "EVT.A.19-20"
		eventType = "coupon.redemption.ineligible"
		if strings.TrimSpace(input.ReasonCode) == "" {
			return Redemption{}, reliability.Event{}, oops.In("coupon_redemption").Code("coupon.redemption_reason_required").New("an ineligible evaluation requires a reason code")
		}
	}
	resultRef := shared.ExternalRef{Context: "coupon", Type: "redemption", ID: input.RedemptionID}
	redemption := Redemption{
		ID: input.RedemptionID, UserCouponID: input.UserCouponID, CampaignID: input.CampaignID,
		UserID: input.UserID, OrderID: input.OrderID, OperationType: "validate", BusinessKey: input.BusinessKey,
		Status: status, ReasonCode: input.ReasonCode, PolicyVersion: input.PolicyVersion,
		OrderSnapshot: input.OrderSnapshot, OrderSnapshotHash: input.OrderSnapshotHash, EvaluatedAt: input.EvaluatedAt,
		Discount: input.Discount, FinalOrderAmount: input.FinalOrderAmount, CostShares: input.CostShares,
		ResultRef: resultRef,
	}
	return redemption, event(redemption, eventID, eventType, input.EvaluatedAt), nil
}

func (r *Redemption) Reserve(expectedVersion int64, reservedUntil, now time.Time) (reliability.Event, error) {
	if r.Version != expectedVersion {
		return reliability.Event{}, versionConflict(r.ID, expectedVersion, r.Version)
	}
	if r.Status != StatusEvaluated && r.Status != StatusReleased {
		return reliability.Event{}, invalidTransition(r.Status, StatusReserved)
	}
	if !reservedUntil.After(now) {
		return reliability.Event{}, oops.In("coupon_redemption").Code("coupon.redemption_reservation_expired").New("reservation deadline must be in the future")
	}
	r.Status = StatusReserved
	r.OperationType = "reserve"
	r.ReservedUntil = &reservedUntil
	r.Version++
	return event(*r, "EVT.A.19-21", "coupon.redemption.reserved", now), nil
}

func (r *Redemption) Confirm(expectedVersion int64, resultRef shared.ExternalRef, resultSnapshot any, reasonCode string, now time.Time) ([]reliability.Event, error) {
	if r.Version != expectedVersion {
		return nil, versionConflict(r.ID, expectedVersion, r.Version)
	}
	if r.Status != StatusReserved {
		return nil, invalidTransition(r.Status, StatusConfirmed)
	}
	if err := resultRef.Validate(); err != nil {
		return nil, err
	}
	r.Status = StatusConfirmed
	r.OperationType = "confirm"
	r.ResultRef = resultRef
	r.ResultSnapshot = resultSnapshot
	r.ReasonCode = reasonCode
	r.ConfirmedAt = &now
	r.Version++
	return []reliability.Event{
		event(*r, "EVT.A.19-22", "coupon.redemption.confirmed", now),
		event(*r, "EVT.A.19-28", "coupon.cost_attribution.recorded", now),
	}, nil
}

func (r *Redemption) Release(expectedVersion int64, resultRef shared.ExternalRef, resultSnapshot any, reasonCode string, now time.Time) (reliability.Event, error) {
	if r.Version != expectedVersion {
		return reliability.Event{}, versionConflict(r.ID, expectedVersion, r.Version)
	}
	if r.Status != StatusReserved {
		return reliability.Event{}, invalidTransition(r.Status, StatusReleased)
	}
	if err := resultRef.Validate(); err != nil {
		return reliability.Event{}, err
	}
	r.Status = StatusReleased
	r.OperationType = "release"
	r.ResultRef = resultRef
	r.ResultSnapshot = resultSnapshot
	r.ReasonCode = reasonCode
	r.ReleasedAt = &now
	r.Version++
	return event(*r, "EVT.A.19-23", "coupon.redemption.released", now), nil
}

func (r *Redemption) Reclaim(expectedVersion int64, resultRef shared.ExternalRef, resultSnapshot any, reasonCode string, now time.Time) ([]reliability.Event, error) {
	if r.Version != expectedVersion {
		return nil, versionConflict(r.ID, expectedVersion, r.Version)
	}
	if r.Status != StatusConfirmed {
		return nil, invalidTransition(r.Status, StatusReclaimed)
	}
	if err := resultRef.Validate(); err != nil {
		return nil, err
	}
	r.Status = StatusReclaimed
	r.OperationType = "reclaim"
	r.ResultRef = resultRef
	r.ResultSnapshot = resultSnapshot
	r.ReasonCode = reasonCode
	r.ReclaimedAt = &now
	r.Version++
	return []reliability.Event{
		event(*r, "EVT.A.19-24", "coupon.redemption.reclaimed", now),
		event(*r, "EVT.A.19-28", "coupon.cost_attribution.recorded", now),
	}, nil
}

func validateAmounts(discount, final shared.Money, shares []CostShare) error {
	if err := discount.Validate(); err != nil {
		return err
	}
	if err := final.Validate(); err != nil {
		return err
	}
	if discount.Currency != final.Currency {
		return oops.In("coupon_redemption").Code("coupon.redemption_currency_mismatch").New("discount and final order amount currencies must match")
	}
	want := new(big.Rat)
	_, _ = want.SetString(discount.Amount)
	got := new(big.Rat)
	for _, share := range shares {
		if err := share.Amount.Validate(); err != nil {
			return err
		}
		if share.Amount.Currency != discount.Currency {
			return oops.In("coupon_redemption").Code("coupon.redemption_cost_currency_mismatch").New("cost share currency must match the discount currency")
		}
		amount := new(big.Rat)
		_, _ = amount.SetString(share.Amount.Amount)
		got.Add(got, amount)
	}
	if got.Cmp(want) != 0 {
		return oops.In("coupon_redemption").Code("coupon.redemption_cost_mismatch").New("cost share total must equal the discount amount")
	}
	return nil
}

func event(r Redemption, documentID, eventType string, at time.Time) reliability.Event {
	return reliability.Event{
		ID: uuid.New(), DocumentID: documentID, Type: eventType,
		AggregateType: "CouponRedemption", AggregateID: r.ID, AggregateVersion: r.Version,
		PayloadSchemaVersion: 1, OccurredAt: at,
		Data: map[string]any{
			"redemption_id": r.ID, "user_coupon_id": r.UserCouponID, "campaign_id": r.CampaignID,
			"user_id":   r.UserID,
			"order_ref": shared.ExternalRef{Context: "order", Type: "order", ID: r.OrderID},
			"status":    r.Status, "result_ref": r.ResultRef, "policy_version": r.PolicyVersion,
			"discount": r.Discount, "final_order_amount": r.FinalOrderAmount,
			"cost_shares": r.CostShares, "evaluated_at": r.EvaluatedAt,
			"reserved_until": r.ReservedUntil,
		},
	}
}

func versionConflict(id string, expected, actual int64) error {
	return oops.In("coupon_redemption").Code("coupon.version_conflict").With("redemption_id", id, "expected_version", expected, "actual_version", actual).New("coupon redemption version does not match")
}

func invalidTransition(from, to Status) error {
	return oops.In("coupon_redemption").Code("coupon.redemption_transition_invalid").With("from", from, "to", to).New("coupon redemption state transition is not allowed")
}
