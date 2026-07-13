package operations

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type ScopeType string

const (
	ScopeCampaign  ScopeType = "campaign"
	ScopeDrop      ScopeType = "drop"
	ScopeUserGroup ScopeType = "user_group"
)

type Scope struct {
	Type ScopeType `json:"type"`
	Ref  string    `json:"ref"`
}

func (s Scope) Validate() error {
	switch s.Type {
	case ScopeCampaign, ScopeDrop, ScopeUserGroup:
	default:
		return oops.In("coupon_operations").Code("coupon.operational_scope_invalid").New("coupon operational scope type is invalid")
	}
	if strings.TrimSpace(s.Ref) == "" {
		return oops.In("coupon_operations").Code("coupon.operational_scope_ref_required").New("coupon operational scope reference is required")
	}
	return nil
}

type Notice struct {
	Message       string    `json:"message,omitempty"`
	Active        bool      `json:"active"`
	EffectiveFrom time.Time `json:"effectiveFrom,omitempty"`
}

type Control struct {
	ID                  string    `json:"controlId"`
	Scopes              []Scope   `json:"scopes"`
	Active              bool      `json:"active"`
	EffectiveFrom       time.Time `json:"effectiveFrom"`
	BlockIssuance       bool      `json:"blockIssuance"`
	BlockRedemption     bool      `json:"blockRedemption"`
	Notice              Notice    `json:"notice"`
	OperationRequestRef string    `json:"operationRequestRef"`
	ApprovalRef         string    `json:"approvalRef"`
	ReasonCode          string    `json:"reasonCode,omitempty"`
	Version             int64     `json:"version"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type Stop struct {
	ControlID           string
	Scopes              []Scope
	Active              bool
	EffectiveFrom       time.Time
	BlockIssuance       bool
	BlockRedemption     bool
	OperationRequestRef string
	ApprovalRef         string
	ReasonCode          string
	AppliedAt           time.Time
}

var controlIDPattern = regexp.MustCompile(`^ctrl_[A-Za-z0-9_-]{8,120}$`)

func ApplyStop(input Stop) (Control, reliability.Event, error) {
	control := Control{
		ID: input.ControlID, Scopes: append([]Scope(nil), input.Scopes...), Active: input.Active,
		EffectiveFrom: input.EffectiveFrom, BlockIssuance: input.BlockIssuance,
		BlockRedemption: input.BlockRedemption, OperationRequestRef: input.OperationRequestRef,
		ApprovalRef: input.ApprovalRef, ReasonCode: input.ReasonCode,
		CreatedAt: input.AppliedAt, UpdatedAt: input.AppliedAt,
	}
	if err := control.Validate(); err != nil {
		return Control{}, reliability.Event{}, err
	}
	return control, event(control, "EVT.A.19-25", "coupon.operational_stop.applied", input.AppliedAt), nil
}

func (c Control) Validate() error {
	if !controlIDPattern.MatchString(c.ID) || len(c.Scopes) == 0 || c.EffectiveFrom.IsZero() ||
		strings.TrimSpace(c.OperationRequestRef) == "" || strings.TrimSpace(c.ApprovalRef) == "" ||
		c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return oops.In("coupon_operations").Code("coupon.operational_control_invalid").New("coupon operational control is incomplete")
	}
	if !c.BlockIssuance && !c.BlockRedemption && strings.TrimSpace(c.Notice.Message) == "" {
		return oops.In("coupon_operations").Code("coupon.operational_control_empty").New("coupon operational control must block an operation or provide a notice")
	}
	seen := make(map[Scope]struct{}, len(c.Scopes))
	for _, scope := range c.Scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
		if _, exists := seen[scope]; exists {
			return oops.In("coupon_operations").Code("coupon.operational_scope_duplicate").New("coupon operational control contains a duplicate scope")
		}
		seen[scope] = struct{}{}
	}
	if c.Notice.Active || strings.TrimSpace(c.Notice.Message) != "" {
		return c.Notice.validate()
	}
	return nil
}

type NoticeUpdate struct {
	ExpectedVersion int64
	Message         string
	EffectiveFrom   time.Time
	Active          bool
	AppliedAt       time.Time
}

func (c *Control) ApplyNotice(input NoticeUpdate) (reliability.Event, error) {
	if c.Version != input.ExpectedVersion {
		return reliability.Event{}, oops.In("coupon_operations").Code("coupon.version_conflict").With("control_id", c.ID, "expected_version", input.ExpectedVersion, "actual_version", c.Version).New("coupon operational control version does not match")
	}
	notice := Notice{Message: input.Message, EffectiveFrom: input.EffectiveFrom, Active: input.Active}
	if err := notice.validate(); err != nil {
		return reliability.Event{}, err
	}
	if input.AppliedAt.IsZero() {
		return reliability.Event{}, oops.In("coupon_operations").Code("coupon.notice_applied_at_required").New("coupon notice applied time is required")
	}
	c.Notice = notice
	c.Version++
	c.UpdatedAt = input.AppliedAt
	return event(*c, "EVT.A.19-38", "coupon.read_only_notice.applied", input.AppliedAt), nil
}

func (n Notice) validate() error {
	message := strings.TrimSpace(n.Message)
	if message == "" || utf8.RuneCountInString(message) > 500 || n.EffectiveFrom.IsZero() {
		return oops.In("coupon_operations").Code("coupon.notice_invalid").New("coupon read-only notice requires a message of at most 500 characters and an effective time")
	}
	return nil
}

func event(control Control, documentID, eventType string, at time.Time) reliability.Event {
	return reliability.Event{
		ID: uuid.New(), DocumentID: documentID, Type: eventType,
		AggregateType: "CouponOperationalControl", AggregateID: control.ID, AggregateVersion: control.Version,
		PayloadSchemaVersion: 1, OccurredAt: at,
		Data: map[string]any{
			"control_id": control.ID, "scopes": control.Scopes, "active": control.Active,
			"effective_from": control.EffectiveFrom, "block_issuance": control.BlockIssuance,
			"block_redemption": control.BlockRedemption, "notice": control.Notice,
			"operation_request_ref": control.OperationRequestRef, "approval_ref": control.ApprovalRef,
		},
	}
}
