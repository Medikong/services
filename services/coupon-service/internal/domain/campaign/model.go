package campaign

import (
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

type Status string

const (
	StatusDraft       Status = "draft"
	StatusUnderReview Status = "under_review"
	StatusApproved    Status = "approved"
	StatusRejected    Status = "rejected"
	StatusHeld        Status = "held"
	StatusActive      Status = "active"
	StatusEnded       Status = "ended"
)

type BenefitType string

const (
	BenefitFixedAmount BenefitType = "fixed_amount"
	BenefitPercentage  BenefitType = "percentage"
	BenefitShippingFee BenefitType = "shipping_fee"
)

type Benefit struct {
	ID                string        `json:"benefitId"`
	PolicyVersion     int64         `json:"policyVersion"`
	Type              BenefitType   `json:"benefitType"`
	Amount            *shared.Money `json:"amount,omitempty"`
	Percentage        string        `json:"percentage,omitempty"`
	MaxDiscountAmount *shared.Money `json:"maxDiscountAmount,omitempty"`
	Currency          string        `json:"currency"`
}

func (b Benefit) Validate() error {
	if strings.TrimSpace(b.ID) == "" || b.PolicyVersion < 1 {
		return oops.In("coupon_campaign").Code("campaign.benefit_invalid").New("benefit id and policy version are required")
	}
	switch b.Type {
	case BenefitFixedAmount:
		if b.Amount == nil || b.Percentage != "" {
			return oops.In("coupon_campaign").Code("campaign.benefit_invalid").New("fixed amount benefit requires only amount")
		}
		return b.Amount.Validate()
	case BenefitPercentage:
		if strings.TrimSpace(b.Percentage) == "" || b.Amount != nil {
			return oops.In("coupon_campaign").Code("campaign.benefit_invalid").New("percentage benefit requires only percentage")
		}
		percentage := new(big.Rat)
		oneHundred := big.NewRat(100, 1)
		if _, ok := percentage.SetString(b.Percentage); !ok || percentage.Sign() < 0 || percentage.Cmp(oneHundred) > 0 {
			return oops.In("coupon_campaign").Code("campaign.benefit_invalid").New("percentage benefit must be between zero and one hundred")
		}
		if b.MaxDiscountAmount != nil {
			return b.MaxDiscountAmount.Validate()
		}
		return nil
	case BenefitShippingFee:
		if b.Amount != nil || b.Percentage != "" || b.MaxDiscountAmount != nil {
			return oops.In("coupon_campaign").Code("campaign.benefit_invalid").New("shipping fee benefit does not accept discount values")
		}
		return nil
	default:
		return oops.In("coupon_campaign").Code("campaign.benefit_type_invalid").New("benefit type is not supported")
	}
}

type ApplicabilityPolicy struct {
	ID             string          `json:"policyId"`
	PolicyVersion  int64           `json:"policyVersion"`
	TargetType     string          `json:"targetType"`
	TargetRef      string          `json:"targetRef"`
	Inclusion      string          `json:"inclusion"`
	ConditionType  string          `json:"conditionType"`
	ConditionValue json.RawMessage `json:"conditionValue"`
	EffectiveFrom  time.Time       `json:"effectiveFrom"`
	SnapshotLabel  string          `json:"snapshotLabel"`
}

func (p ApplicabilityPolicy) Validate() error {
	if strings.TrimSpace(p.ID) == "" || p.PolicyVersion < 1 || strings.TrimSpace(p.TargetType) == "" || strings.TrimSpace(p.TargetRef) == "" || (p.Inclusion != "include" && p.Inclusion != "exclude") || strings.TrimSpace(p.ConditionType) == "" || len(p.ConditionValue) == 0 || p.EffectiveFrom.IsZero() || strings.TrimSpace(p.SnapshotLabel) == "" {
		return oops.In("coupon_campaign").Code("campaign.applicability_invalid").New("applicability policy is incomplete")
	}
	if !json.Valid(p.ConditionValue) {
		return oops.In("coupon_campaign").Code("campaign.applicability_invalid").New("applicability condition must be valid json")
	}
	return nil
}

type QuantityLimit struct {
	TotalQuantity int64     `json:"totalQuantity"`
	PerUserLimit  int64     `json:"perUserLimit"`
	ClaimStartsAt time.Time `json:"claimStartsAt"`
	ClaimEndsAt   time.Time `json:"claimEndsAt"`
}

func (l QuantityLimit) Validate() error {
	if l.TotalQuantity <= 0 || l.PerUserLimit <= 0 || l.PerUserLimit > l.TotalQuantity || l.ClaimStartsAt.IsZero() || !l.ClaimStartsAt.Before(l.ClaimEndsAt) {
		return oops.In("coupon_campaign").Code("campaign.quantity_limit_invalid").New("quantity limit and claim period are invalid")
	}
	return nil
}

type Campaign struct {
	ID                   string                  `json:"campaignId"`
	DisplayName          string                  `json:"displayName"`
	Description          string                  `json:"description,omitempty"`
	Status               Status                  `json:"status"`
	StartsAt             time.Time               `json:"startsAt"`
	EndsAt               time.Time               `json:"endsAt"`
	CurrentPolicyVersion int64                   `json:"currentPolicyVersion"`
	TotalQuantity        int64                   `json:"totalQuantity"`
	ReservedQuantity     int64                   `json:"reservedQuantity"`
	ConfirmedQuantity    int64                   `json:"confirmedQuantity"`
	PerUserLimit         int64                   `json:"perUserLimit"`
	ClaimStartsAt        *time.Time              `json:"claimStartsAt,omitempty"`
	ClaimEndsAt          *time.Time              `json:"claimEndsAt,omitempty"`
	IssuerAndFunding     shared.IssuerAndFunding `json:"issuerAndFunding"`
	ApprovalRef          string                  `json:"approvalRef,omitempty"`
	OwnerSnapshot        shared.SnapshotRef      `json:"ownerSnapshot"`
	ExternalBusinessRef  string                  `json:"externalBusinessRef,omitempty"`
	Version              int64                   `json:"version"`
	CreatedAt            time.Time               `json:"createdAt"`
	UpdatedAt            time.Time               `json:"updatedAt"`
	Benefits             []Benefit               `json:"benefits"`
	Applicability        []ApplicabilityPolicy   `json:"applicability"`
}

func (c Campaign) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.DisplayName) == "" || c.CurrentPolicyVersion < 1 || c.Version < 0 || c.StartsAt.IsZero() || !c.StartsAt.Before(c.EndsAt) {
		return oops.In("coupon_campaign").Code("campaign.invalid").New("campaign identity, version, and period are required")
	}
	if !c.Status.Valid() {
		return oops.In("coupon_campaign").Code("campaign.status_invalid").New("campaign status is not supported")
	}
	if c.TotalQuantity < 0 || c.ReservedQuantity < 0 || c.ConfirmedQuantity < 0 || c.ReservedQuantity+c.ConfirmedQuantity > c.TotalQuantity {
		return oops.In("coupon_campaign").Code("campaign.quantity_invalid").New("campaign quantities violate the total limit")
	}
	if err := c.IssuerAndFunding.Validate(); err != nil {
		return err
	}
	if err := c.OwnerSnapshot.Validate(); err != nil {
		return err
	}
	if c.PerUserLimit < 0 || (c.TotalQuantity == 0 && c.PerUserLimit != 0) ||
		(c.TotalQuantity > 0 && (c.PerUserLimit <= 0 || c.ClaimStartsAt == nil || c.ClaimEndsAt == nil)) ||
		(c.ClaimStartsAt != nil && c.ClaimEndsAt != nil && !c.ClaimStartsAt.Before(*c.ClaimEndsAt)) {
		return oops.In("coupon_campaign").Code("campaign.quantity_limit_invalid").New("campaign claim limit is invalid")
	}
	if len(c.Benefits) == 0 || len(c.Applicability) == 0 {
		return oops.In("coupon_campaign").Code("campaign.policy_incomplete").New("campaign requires benefit and applicability policy")
	}
	for _, benefit := range c.Benefits {
		if err := benefit.Validate(); err != nil || benefit.PolicyVersion != c.CurrentPolicyVersion {
			if err != nil {
				return err
			}
			return oops.In("coupon_campaign").Code("campaign.policy_version_mismatch").New("benefit policy version does not match campaign")
		}
	}
	for _, policy := range c.Applicability {
		if err := policy.Validate(); err != nil || policy.PolicyVersion != c.CurrentPolicyVersion {
			if err != nil {
				return err
			}
			return oops.In("coupon_campaign").Code("campaign.policy_version_mismatch").New("applicability policy version does not match campaign")
		}
	}
	return nil
}

func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusUnderReview, StatusApproved, StatusRejected, StatusHeld, StatusActive, StatusEnded:
		return true
	default:
		return false
	}
}

func (c Campaign) Review(decision Status) (Campaign, error) {
	if c.Status != StatusUnderReview {
		return Campaign{}, ErrInvalidTransition
	}
	switch decision {
	case StatusApproved, StatusRejected, StatusHeld:
		if decision == StatusApproved && (c.TotalQuantity <= 0 || c.PerUserLimit <= 0 || c.ClaimStartsAt == nil || c.ClaimEndsAt == nil) {
			return Campaign{}, oops.In("coupon_campaign").Code("campaign.issuance_not_configured").New("campaign issuance limit must be configured before approval")
		}
		c.Status = decision
		c.Version++
		return c, nil
	default:
		return Campaign{}, ErrInvalidTransition
	}
}

func (c Campaign) CanReserve(quantity int64, at time.Time) error {
	if quantity <= 0 {
		return ErrInvalidQuantity
	}
	if !c.IsIssuableAt(at) {
		return ErrCampaignInactive
	}
	if c.ReservedQuantity+c.ConfirmedQuantity+quantity > c.TotalQuantity {
		return ErrQuantityUnavailable
	}
	return nil
}

// IsIssuableAt derives the effective lifecycle from the approved policy and
// its validity window because BC.A.19 has no separate activation command.
func (c Campaign) IsIssuableAt(at time.Time) bool {
	return (c.Status == StatusApproved || c.Status == StatusActive) && !at.Before(c.StartsAt) && at.Before(c.EndsAt)
}

type ReservationState string

const (
	ReservationReserved  ReservationState = "reserved"
	ReservationConfirmed ReservationState = "confirmed"
	ReservationReleased  ReservationState = "released"
	ReservationRejected  ReservationState = "rejected"
)

type QuantityReservation struct {
	CampaignID     string           `json:"campaignId"`
	IssueRequestID string           `json:"issueRequestId"`
	Quantity       int64            `json:"quantity"`
	State          ReservationState `json:"state"`
	ResultRef      string           `json:"resultRef"`
	ReservedAt     time.Time        `json:"reservedAt"`
	DecidedAt      *time.Time       `json:"decidedAt,omitempty"`
}

var (
	ErrNotFound            = oops.In("coupon_campaign").Code("campaign.not_found").New("campaign was not found")
	ErrVersionConflict     = oops.In("coupon_campaign").Code("campaign.version_conflict").New("campaign version does not match")
	ErrInvalidTransition   = oops.In("coupon_campaign").Code("campaign.transition_invalid").New("campaign transition is not allowed")
	ErrInvalidQuantity     = oops.In("coupon_campaign").Code("campaign.quantity_invalid").New("quantity must be positive")
	ErrCampaignInactive    = oops.In("coupon_campaign").Code("campaign.inactive").New("campaign is not active at the requested time")
	ErrIssuanceBlocked     = oops.In("coupon_campaign").Code("campaign.issuance_blocked").New("campaign issuance is blocked by an operational control")
	ErrQuantityUnavailable = oops.In("coupon_campaign").Code("campaign.quantity_unavailable").New("campaign quantity is unavailable")
	ErrIdempotencyConflict = oops.In("coupon_campaign").Code("campaign.idempotency_conflict").New("idempotency key was reused with a different request")
	ErrCommandInProgress   = oops.In("coupon_campaign").Code("campaign.command_in_progress").New("the same command is already processing")
)
