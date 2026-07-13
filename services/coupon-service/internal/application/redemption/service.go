package redemption

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	domainoperations "github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	domainredemption "github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

type Metadata struct {
	IdempotencyKey string
	BusinessKey    string
	CorrelationID  string
	CausationID    string
	TraceID        string
	RequestedAt    time.Time
	LeaseUntil     time.Time
	ExpiresAt      time.Time
}

type OrderItem struct {
	ProductRef  shared.ExternalRef  `json:"productRef"`
	DropRef     *shared.ExternalRef `json:"dropRef,omitempty"`
	SellerRef   shared.ExternalRef  `json:"sellerRef"`
	CategoryRef *shared.ExternalRef `json:"categoryRef,omitempty"`
	Quantity    int64               `json:"quantity"`
	UnitPrice   shared.Money        `json:"unitPrice"`
}

type OrderSnapshot struct {
	Ref         shared.SnapshotRef `json:"snapshotRef"`
	OrderID     string             `json:"orderId"`
	UserID      string             `json:"userId"`
	Items       []OrderItem        `json:"items"`
	ShippingFee shared.Money       `json:"shippingFee"`
}

type ValidateInput struct {
	UserCouponID      string        `json:"userCouponId"`
	Order             OrderSnapshot `json:"orderSnapshot"`
	PolicyVersion     int64         `json:"policyVersion"`
	StackingPolicyRef string        `json:"stackingPolicyRef"`
	EvaluatedAt       time.Time     `json:"evaluatedAt"`
}

type ReserveInput struct {
	RedemptionID    string `json:"redemptionId"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type TransitionInput struct {
	RedemptionID    string              `json:"redemptionId"`
	ExpectedVersion int64               `json:"expectedVersion"`
	ResultRef       shared.ExternalRef  `json:"resultRef"`
	ResultSnapshot  *shared.SnapshotRef `json:"resultSnapshot,omitempty"`
	ReasonCode      string              `json:"reasonCode"`
}

type ReplayPayloadStore interface {
	Load(context.Context, string) ([]byte, error)
}

type ReplayInput struct {
	RecoveryID            string
	AttemptID             string
	BusinessKey           string
	RedemptionID          string
	OriginalOperationType recovery.OperationType
	OriginalPayloadRef    string
	OriginalPayloadHash   string
}

type ReplayPayload struct {
	Operation       recovery.OperationType `json:"operation"`
	BusinessKey     string                 `json:"businessKey"`
	RedemptionID    string                 `json:"redemptionId"`
	ExpectedVersion int64                  `json:"expectedVersion"`
	ReservedUntil   *time.Time             `json:"reservedUntil,omitempty"`
	ResultRef       *shared.ExternalRef    `json:"resultRef,omitempty"`
	ResultSnapshot  *shared.SnapshotRef    `json:"resultSnapshot,omitempty"`
	ReasonCode      string                 `json:"reasonCode,omitempty"`
}

type RecoveryResultCommand struct {
	RecoveryID  string
	AttemptID   string
	BusinessKey string
	Kind        recovery.ResultKind
	ResultRef   string
	FailureCode string
}

type evaluationPolicy struct {
	Benefits      []campaign.Benefit
	Applicability []campaign.ApplicabilityPolicy
	Funding       shared.IssuerAndFunding
}

type grantSnapshot struct {
	Benefit struct {
		Type              campaign.BenefitType `json:"type"`
		Amount            *shared.Money        `json:"amount,omitempty"`
		Percentage        string               `json:"percentage,omitempty"`
		MaxDiscountAmount *shared.Money        `json:"maxDiscountAmount,omitempty"`
	} `json:"benefit"`
	Applicability struct {
		PolicySchemaVersion int                  `json:"policySchemaVersion"`
		IncludeTargets      []shared.ExternalRef `json:"includeTargets"`
		ExcludeTargets      []shared.ExternalRef `json:"excludeTargets"`
		MinimumOrderAmount  *shared.Money        `json:"minimumOrderAmount,omitempty"`
		StackingPolicyRef   string               `json:"stackingPolicyRef,omitempty"`
	} `json:"applicability"`
	IssuerAndFunding shared.IssuerAndFunding `json:"issuerAndFunding"`
}

type Dependencies struct {
	Redemptions    domainredemption.Repository
	UserCoupons    usercoupon.Repository
	Campaigns      campaign.Repository
	Controls       domainoperations.Repository
	Users          ports.UserEligibilityPort
	Products       ports.ProductSnapshotPort
	Drops          ports.DropSnapshotPort
	Sellers        ports.SellerCatalogSnapshotPort
	Orders         ports.OrderSnapshotPort
	Payments       ports.PaymentResultPort
	Cases          ports.CSCasePort
	ReplayPayloads ReplayPayloadStore
}

type Service struct {
	deps           Dependencies
	reservationTTL time.Duration
	now            func() time.Time
}

func NewService(deps Dependencies, reservationTTL time.Duration, now func() time.Time) (*Service, error) {
	if deps.Redemptions == nil || deps.UserCoupons == nil || deps.Campaigns == nil || deps.Controls == nil ||
		deps.Users == nil || deps.Products == nil || deps.Drops == nil || deps.Sellers == nil || deps.Orders == nil ||
		deps.Payments == nil || deps.Cases == nil || deps.ReplayPayloads == nil {
		return nil, inputError("coupon.redemption_dependency_required", "redemption application dependencies are required")
	}
	if reservationTTL <= 0 || now == nil {
		return nil, inputError("coupon.redemption_config_invalid", "reservation ttl and clock are required")
	}
	return &Service{deps: deps, reservationTTL: reservationTTL, now: now}, nil
}

func (s *Service) Validate(ctx context.Context, input ValidateInput, metadata Metadata) (domainredemption.Redemption, error) {
	if strings.TrimSpace(input.UserCouponID) == "" || input.PolicyVersion < 1 {
		return domainredemption.Redemption{}, inputError("coupon.validation_input_invalid", "user coupon and policy version are required")
	}
	if strings.TrimSpace(input.StackingPolicyRef) == "" {
		return domainredemption.Redemption{}, inputError("coupon.stacking_policy_required", "stackingPolicyRef is required while HOTSPOT.A.19-08 remains open")
	}
	if err := input.Order.validate(); err != nil {
		return domainredemption.Redemption{}, err
	}
	if err := s.verifyOrderReferences(ctx, input.Order); err != nil {
		return domainredemption.Redemption{}, err
	}

	evaluatedAt := input.EvaluatedAt
	if evaluatedAt.IsZero() {
		evaluatedAt = s.now().UTC()
	}
	eligibility, err := s.deps.Users.Snapshot(ctx, input.Order.UserID, evaluatedAt)
	if err != nil {
		return domainredemption.Redemption{}, oops.In("coupon_redemption_application").Code("coupon.user_snapshot_failed").Wrap(err)
	}
	if err := eligibility.Snapshot.Validate(); err != nil {
		return domainredemption.Redemption{}, oops.In("coupon_redemption_application").Code("coupon.user_snapshot_invalid").Wrap(err)
	}

	owned, err := s.deps.UserCoupons.Get(ctx, input.UserCouponID)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	policy, err := s.deps.Campaigns.Get(ctx, owned.CampaignID)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	_, consuming, err := s.deps.Redemptions.FindConsumingByUserCoupon(ctx, owned.ID)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	total, err := input.Order.total()
	if err != nil {
		return domainredemption.Redemption{}, err
	}

	reason := couponReason(owned, policy, input, eligibility.Eligible, evaluatedAt)
	if consuming {
		reason = "user_coupon_already_consuming"
	}
	if reason == "" {
		blocked, blockErr := s.redemptionBlocked(ctx, policy.ID, eligibility.Snapshot, input.Order, evaluatedAt)
		if blockErr != nil {
			return domainredemption.Redemption{}, blockErr
		}
		if blocked {
			reason = "coupon_operational_stop"
		}
	}
	var frozen evaluationPolicy
	if reason == "" {
		frozen, err = policyForCoupon(owned, policy)
		if err != nil {
			return domainredemption.Redemption{}, err
		}
		reason, err = policyReason(frozen.Applicability, input, total, evaluatedAt)
		if err != nil {
			return domainredemption.Redemption{}, err
		}
	}

	discount := shared.Money{Amount: "0", Currency: total.Currency}
	final := total
	var shares []domainredemption.CostShare
	if reason == "" {
		discount, final, err = calculateDiscount(frozen.Benefits, input.Order, total)
		if err != nil {
			return domainredemption.Redemption{}, err
		}
		shares, err = calculateCostShares(discount, frozen.Funding)
		if err != nil {
			return domainredemption.Redemption{}, err
		}
	}

	command, err := newCommand("CMD.A.19-09", "coupon.redemption.validate", []string{
		input.Order.OrderID, input.UserCouponID, input.Order.Ref.SourceVersion,
	}, input, metadata)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	evaluation := domainredemption.Evaluation{
		RedemptionID: stableID("redm", command.BusinessKey), UserCouponID: owned.ID,
		CampaignID: policy.ID, UserID: input.Order.UserID, OrderID: input.Order.OrderID,
		BusinessKey: command.BusinessKey, Eligible: reason == "", ReasonCode: reason,
		PolicyVersion: int(input.PolicyVersion), OrderSnapshot: input.Order,
		OrderSnapshotHash: input.Order.Ref.PayloadHash, Discount: discount,
		FinalOrderAmount: final, CostShares: shares, EvaluatedAt: evaluatedAt,
	}
	return s.deps.Redemptions.Evaluate(ctx, evaluation, command)
}

func (s *Service) Reserve(ctx context.Context, input ReserveInput, metadata Metadata) (domainredemption.Redemption, error) {
	if strings.TrimSpace(input.RedemptionID) == "" || input.ExpectedVersion < 0 {
		return domainredemption.Redemption{}, inputError("coupon.reserve_input_invalid", "redemption id and expected version are required")
	}
	current, err := s.deps.Redemptions.Find(ctx, input.RedemptionID)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	blocked, err := s.scopeBlocked(ctx, domainoperations.Scope{Type: domainoperations.ScopeCampaign, Ref: current.CampaignID}, metadata.RequestedAt)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	if blocked {
		return domainredemption.Redemption{}, inputError("coupon.redemption_operational_stop", "new coupon redemption reservations are blocked")
	}
	command, err := newCommand("CMD.A.19-10", "coupon.redemption.reserve", []string{input.RedemptionID}, input, metadata)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	return s.deps.Redemptions.Reserve(ctx, input.RedemptionID, input.ExpectedVersion, metadata.RequestedAt.Add(s.reservationTTL), command)
}

func (s *Service) Confirm(ctx context.Context, input TransitionInput, metadata Metadata) (domainredemption.Redemption, error) {
	if err := validateTransition(input); err != nil {
		return domainredemption.Redemption{}, err
	}
	current, err := s.deps.Redemptions.Find(ctx, input.RedemptionID)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	if err := s.deps.Payments.VerifyPaymentResult(ctx, input.ResultRef, input.ResultSnapshot, paymentBinding(current)); err != nil {
		return domainredemption.Redemption{}, oops.In("coupon_redemption_application").Code("coupon.payment_result_verification_failed").Wrap(err)
	}
	command, err := newCommand("CMD.A.19-11", "coupon.redemption.confirm", []string{input.RedemptionID}, input, metadata)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	return s.deps.Redemptions.Confirm(ctx, input.RedemptionID, input.ExpectedVersion, input.ResultRef, input.ResultSnapshot, input.ReasonCode, command)
}

func (s *Service) Release(ctx context.Context, input TransitionInput, metadata Metadata) (domainredemption.Redemption, error) {
	if err := validateTransition(input); err != nil {
		return domainredemption.Redemption{}, err
	}
	command, err := newCommand("CMD.A.19-12", "coupon.redemption.release", []string{input.RedemptionID}, input, metadata)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	return s.deps.Redemptions.Release(ctx, input.RedemptionID, input.ExpectedVersion, input.ResultRef, input.ResultSnapshot, input.ReasonCode, command)
}

func (s *Service) Reclaim(ctx context.Context, input TransitionInput, metadata Metadata) (domainredemption.Redemption, error) {
	if err := validateTransition(input); err != nil {
		return domainredemption.Redemption{}, err
	}
	current, err := s.deps.Redemptions.Find(ctx, input.RedemptionID)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	if err := s.verifyReclaimReference(ctx, input.ResultRef, input.ResultSnapshot, current); err != nil {
		return domainredemption.Redemption{}, err
	}
	command, err := newCommand("CMD.A.19-15", "coupon.redemption.reclaim", []string{input.RedemptionID}, input, metadata)
	if err != nil {
		return domainredemption.Redemption{}, err
	}
	return s.deps.Redemptions.Reclaim(ctx, input.RedemptionID, input.ExpectedVersion, input.ResultRef, input.ResultSnapshot, input.ReasonCode, command)
}

func (s *Service) Replay(ctx context.Context, input ReplayInput, metadata Metadata) (RecoveryResultCommand, error) {
	failed := RecoveryResultCommand{
		RecoveryID: input.RecoveryID, AttemptID: input.AttemptID, BusinessKey: input.BusinessKey,
		Kind: recovery.ResultFailed, FailureCode: "coupon_replay_failed",
	}
	if !strings.HasPrefix(input.RecoveryID, "rcvy_") || !strings.HasPrefix(input.AttemptID, "att_") ||
		strings.TrimSpace(input.BusinessKey) == "" || !strings.HasPrefix(input.RedemptionID, "redm_") ||
		strings.TrimSpace(input.OriginalPayloadRef) == "" {
		return failed, inputError("coupon.replay_correlation_invalid", "recovery id, attempt id, business key, redemption id, and payload reference are required")
	}
	if err := validateMetadata(metadata, true); err != nil {
		return failed, err
	}
	raw, err := s.deps.ReplayPayloads.Load(ctx, input.OriginalPayloadRef)
	if err != nil {
		return failed, oops.In("coupon_redemption_application").Code("coupon.replay_payload_load_failed").Wrap(err)
	}
	digest, err := verifyPayloadHash(raw, input.OriginalPayloadHash)
	if err != nil {
		return failed, err
	}
	var payload ReplayPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return failed, oops.In("coupon_redemption_application").Code("coupon.replay_payload_invalid").Wrap(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return failed, inputError("coupon.replay_payload_invalid", "replay payload contains multiple json values")
		}
		return failed, oops.In("coupon_redemption_application").Code("coupon.replay_payload_invalid").Wrap(err)
	}
	if payload.BusinessKey != input.BusinessKey || payload.RedemptionID != input.RedemptionID || payload.Operation != input.OriginalOperationType {
		return failed, inputError("coupon.replay_correlation_mismatch", "immutable replay payload does not match the recovery correlation")
	}
	if err := validateReplayPayload(payload); err != nil {
		return failed, err
	}

	operation, err := replayOperation(payload.Operation)
	if err != nil {
		return failed, err
	}
	current, err := s.deps.Redemptions.Find(ctx, payload.RedemptionID)
	if err != nil {
		return failed, err
	}
	if payload.Operation == recovery.OperationConfirm {
		if err := s.deps.Payments.VerifyPaymentResult(ctx, *payload.ResultRef, payload.ResultSnapshot, paymentBinding(current)); err != nil {
			return failed, oops.In("coupon_redemption_application").Code("coupon.payment_result_verification_failed").Wrap(err)
		}
	}
	if payload.Operation == recovery.OperationReclaim {
		if err := s.verifyReclaimReference(ctx, *payload.ResultRef, payload.ResultSnapshot, current); err != nil {
			return failed, err
		}
	}
	replayBusinessKey := strings.Join([]string{input.RecoveryID, input.AttemptID, input.BusinessKey}, "|")
	command := reliability.Command{
		DocumentID: "CMD.A.19-32", OperationType: "coupon.redemption.replay",
		BusinessKey: replayBusinessKey, RequestHash: digest, CorrelationID: metadata.CorrelationID,
		CausationID: metadata.CausationID, TraceID: metadata.TraceID, LeaseUntil: metadata.LeaseUntil, ExpiresAt: metadata.ExpiresAt,
	}
	outcome, err := s.deps.Redemptions.Replay(ctx, domainredemption.ReplayRequest{
		RecoveryID: input.RecoveryID, AttemptID: input.AttemptID, BusinessKey: input.BusinessKey,
		RedemptionID: payload.RedemptionID, Operation: operation, ExpectedVersion: payload.ExpectedVersion,
		ReservedUntil: payload.ReservedUntil, ResultRef: payload.ResultRef, ResultSnapshot: payload.ResultSnapshot,
		ReasonCode: payload.ReasonCode, ReplayedAt: metadata.RequestedAt,
	}, command)
	if err != nil {
		return failed, err
	}
	kind, err := recoveryResultKind(outcome.ResultKind)
	if err != nil {
		return failed, err
	}
	return RecoveryResultCommand{
		RecoveryID: input.RecoveryID, AttemptID: input.AttemptID, BusinessKey: input.BusinessKey,
		Kind: kind, ResultRef: outcome.ResultRef, FailureCode: outcome.FailureCode,
	}, nil
}

func (s *Service) verifyOrderReferences(ctx context.Context, order OrderSnapshot) error {
	if err := s.deps.Orders.VerifyOrder(ctx, order.Ref); err != nil {
		return oops.In("coupon_redemption_application").Code("coupon.order_snapshot_verification_failed").Wrap(err)
	}
	if err := s.deps.Sellers.VerifySellerOwnership(ctx, order.Ref); err != nil {
		return oops.In("coupon_redemption_application").Code("coupon.seller_snapshot_verification_failed").Wrap(err)
	}
	for _, item := range order.Items {
		if err := s.deps.Products.VerifyProduct(ctx, item.ProductRef, order.Ref); err != nil {
			return oops.In("coupon_redemption_application").Code("coupon.product_snapshot_verification_failed").Wrap(err)
		}
		if item.DropRef != nil {
			if err := s.deps.Drops.VerifyDrop(ctx, *item.DropRef, order.Ref); err != nil {
				return oops.In("coupon_redemption_application").Code("coupon.drop_snapshot_verification_failed").Wrap(err)
			}
		}
	}
	return nil
}

func (s *Service) verifyReclaimReference(ctx context.Context, ref shared.ExternalRef, snapshot *shared.SnapshotRef, redemption domainredemption.Redemption) error {
	if ref.Context == "cs" {
		if err := s.deps.Cases.VerifyCase(ctx, ref.ID, ports.CSCaseBinding{UserID: redemption.UserID, RedemptionID: redemption.ID}); err != nil {
			return oops.In("coupon_redemption_application").Code("coupon.cs_case_verification_failed").Wrap(err)
		}
		return nil
	}
	if err := s.deps.Payments.VerifyPaymentResult(ctx, ref, snapshot, paymentBinding(redemption)); err != nil {
		return oops.In("coupon_redemption_application").Code("coupon.payment_result_verification_failed").Wrap(err)
	}
	return nil
}

func paymentBinding(redemption domainredemption.Redemption) ports.PaymentResultBinding {
	return ports.PaymentResultBinding{RedemptionID: redemption.ID, OrderID: redemption.OrderID}
}

func (s *Service) redemptionBlocked(ctx context.Context, campaignID string, user shared.SnapshotRef, order OrderSnapshot, at time.Time) (bool, error) {
	scopes := []domainoperations.Scope{{Type: domainoperations.ScopeCampaign, Ref: campaignID}}
	if user.SourceRef.Type == "user_group" {
		scopes = append(scopes, domainoperations.Scope{Type: domainoperations.ScopeUserGroup, Ref: user.SourceRef.ID})
	}
	seenDrops := map[string]struct{}{}
	for _, item := range order.Items {
		if item.DropRef == nil {
			continue
		}
		if _, exists := seenDrops[item.DropRef.ID]; exists {
			continue
		}
		seenDrops[item.DropRef.ID] = struct{}{}
		scopes = append(scopes, domainoperations.Scope{Type: domainoperations.ScopeDrop, Ref: item.DropRef.ID})
	}
	for _, scope := range scopes {
		blocked, err := s.scopeBlocked(ctx, scope, at)
		if err != nil || blocked {
			return blocked, err
		}
	}
	return false, nil
}

func (s *Service) scopeBlocked(ctx context.Context, scope domainoperations.Scope, at time.Time) (bool, error) {
	if at.IsZero() {
		return false, inputError("coupon.command_requested_at_required", "command requested time is required")
	}
	controls, err := s.deps.Controls.FindEffective(ctx, scope, at)
	if err != nil {
		return false, err
	}
	for _, control := range controls {
		if control.Active && !at.Before(control.EffectiveFrom) && control.BlockRedemption {
			return true, nil
		}
	}
	return false, nil
}

func (o OrderSnapshot) validate() error {
	if strings.TrimSpace(o.OrderID) == "" || strings.TrimSpace(o.UserID) == "" || len(o.Items) == 0 {
		return inputError("coupon.order_snapshot_invalid", "order id, user id, and order items are required")
	}
	if err := o.Ref.Validate(); err != nil {
		return err
	}
	if o.Ref.SourceRef.ID != o.OrderID {
		return inputError("coupon.order_snapshot_ref_mismatch", "order snapshot reference does not match the order id")
	}
	if err := o.ShippingFee.Validate(); err != nil {
		return err
	}
	for _, item := range o.Items {
		if item.Quantity <= 0 {
			return inputError("coupon.order_item_quantity_invalid", "order item quantity must be positive")
		}
		if err := item.ProductRef.Validate(); err != nil {
			return err
		}
		if err := item.SellerRef.Validate(); err != nil {
			return err
		}
		if item.DropRef != nil {
			if err := item.DropRef.Validate(); err != nil {
				return err
			}
		}
		if item.CategoryRef != nil {
			if err := item.CategoryRef.Validate(); err != nil {
				return err
			}
		}
		if err := item.UnitPrice.Validate(); err != nil {
			return err
		}
		if item.UnitPrice.Currency != o.ShippingFee.Currency {
			return inputError("coupon.order_currency_mismatch", "order item and shipping currencies must match")
		}
	}
	return nil
}

func (o OrderSnapshot) subtotal() (*big.Rat, error) {
	total := new(big.Rat)
	for _, item := range o.Items {
		amount, err := moneyRat(item.UnitPrice)
		if err != nil {
			return nil, err
		}
		total.Add(total, new(big.Rat).Mul(amount, big.NewRat(item.Quantity, 1)))
	}
	return total, nil
}

func (o OrderSnapshot) total() (shared.Money, error) {
	total, err := o.subtotal()
	if err != nil {
		return shared.Money{}, err
	}
	shipping, err := moneyRat(o.ShippingFee)
	if err != nil {
		return shared.Money{}, err
	}
	total.Add(total, shipping)
	amount, err := decimal(total)
	if err != nil {
		return shared.Money{}, err
	}
	return shared.Money{Amount: amount, Currency: o.ShippingFee.Currency}, nil
}

func couponReason(owned usercoupon.Coupon, policy campaign.Campaign, input ValidateInput, userEligible bool, at time.Time) string {
	switch {
	case owned.Status != usercoupon.StatusGranted:
		return "user_coupon_not_granted"
	case owned.UserID != input.Order.UserID:
		return "user_coupon_owner_mismatch"
	case at.Before(owned.UsableFrom) || !at.Before(owned.ExpiresAt):
		return "user_coupon_not_usable"
	case owned.CampaignID != policy.ID:
		return "campaign_reference_mismatch"
	case owned.PolicyVersion != input.PolicyVersion:
		return "policy_version_mismatch"
	case !policy.IsIssuableAt(at):
		return "campaign_inactive"
	case !userEligible:
		return "user_ineligible"
	default:
		return ""
	}
}

func policyForCoupon(owned usercoupon.Coupon, current campaign.Campaign) (evaluationPolicy, error) {
	if frozen, err := decodeGrantSnapshot(owned); err == nil {
		return frozen, nil
	} else if owned.PolicyVersion != current.CurrentPolicyVersion {
		return evaluationPolicy{}, oops.In("coupon_redemption_application").Code("coupon.frozen_policy_snapshot_required").Wrap(err)
	}

	// ponytail: current-version fallback only supports legacy rows; historical versions must use the issue-time snapshot.
	policy := evaluationPolicy{Funding: current.IssuerAndFunding}
	for _, benefit := range current.Benefits {
		if benefit.PolicyVersion == owned.PolicyVersion {
			policy.Benefits = append(policy.Benefits, benefit)
		}
	}
	for _, rule := range current.Applicability {
		if rule.PolicyVersion == owned.PolicyVersion {
			policy.Applicability = append(policy.Applicability, rule)
		}
	}
	if len(policy.Benefits) == 0 || len(policy.Applicability) == 0 {
		return evaluationPolicy{}, inputError("coupon.policy_version_snapshot_missing", "requested coupon policy version is not available")
	}
	return policy, nil
}

func decodeGrantSnapshot(owned usercoupon.Coupon) (evaluationPolicy, error) {
	var snapshot grantSnapshot
	if err := json.Unmarshal(owned.GrantSnapshot, &snapshot); err != nil {
		return evaluationPolicy{}, oops.In("coupon_redemption_application").Code("coupon.grant_snapshot_invalid").Wrap(err)
	}
	benefit := campaign.Benefit{
		ID: "frozen-" + owned.ID, PolicyVersion: owned.PolicyVersion, Type: snapshot.Benefit.Type,
		Amount: snapshot.Benefit.Amount, Percentage: snapshot.Benefit.Percentage,
		MaxDiscountAmount: snapshot.Benefit.MaxDiscountAmount,
	}
	if benefit.Amount != nil {
		benefit.Currency = benefit.Amount.Currency
	} else if benefit.MaxDiscountAmount != nil {
		benefit.Currency = benefit.MaxDiscountAmount.Currency
	}
	if err := benefit.Validate(); err != nil {
		return evaluationPolicy{}, err
	}
	if snapshot.Applicability.PolicySchemaVersion < 1 {
		return evaluationPolicy{}, inputError("coupon.grant_snapshot_invalid", "frozen applicability schema version is required")
	}
	if err := snapshot.IssuerAndFunding.Validate(); err != nil {
		return evaluationPolicy{}, err
	}
	condition, err := json.Marshal(struct {
		MinimumOrderAmount *shared.Money `json:"minimumOrderAmount,omitempty"`
		StackingPolicyRef  string        `json:"stackingPolicyRef,omitempty"`
	}{snapshot.Applicability.MinimumOrderAmount, snapshot.Applicability.StackingPolicyRef})
	if err != nil {
		return evaluationPolicy{}, oops.In("coupon_redemption_application").Code("coupon.grant_snapshot_invalid").Wrap(err)
	}
	rules := make([]campaign.ApplicabilityPolicy, 0, len(snapshot.Applicability.IncludeTargets)+len(snapshot.Applicability.ExcludeTargets))
	appendRule := func(ref shared.ExternalRef, inclusion string) error {
		if err := ref.Validate(); err != nil {
			return err
		}
		rules = append(rules, campaign.ApplicabilityPolicy{
			ID:            "frozen-" + owned.ID + "-" + inclusion + "-" + ref.ID,
			PolicyVersion: owned.PolicyVersion, TargetType: ref.Type, TargetRef: ref.ID,
			Inclusion: inclusion, ConditionType: "all", ConditionValue: condition,
			EffectiveFrom: owned.UsableFrom, SnapshotLabel: "user-coupon:" + owned.ID,
		})
		return nil
	}
	for _, ref := range snapshot.Applicability.IncludeTargets {
		if err := appendRule(ref, "include"); err != nil {
			return evaluationPolicy{}, err
		}
	}
	for _, ref := range snapshot.Applicability.ExcludeTargets {
		if err := appendRule(ref, "exclude"); err != nil {
			return evaluationPolicy{}, err
		}
	}
	if len(snapshot.Applicability.IncludeTargets) == 0 {
		if err := appendRule(shared.ExternalRef{Context: "coupon", Type: "all", ID: "all"}, "include"); err != nil {
			return evaluationPolicy{}, err
		}
	}
	return evaluationPolicy{Benefits: []campaign.Benefit{benefit}, Applicability: rules, Funding: snapshot.IssuerAndFunding}, nil
}

func policyReason(rules []campaign.ApplicabilityPolicy, input ValidateInput, total shared.Money, at time.Time) (string, error) {
	includeFound := false
	includeMatched := false
	for _, rule := range rules {
		if rule.PolicyVersion != input.PolicyVersion || at.Before(rule.EffectiveFrom) {
			continue
		}
		matched, err := targetMatches(rule.TargetType, rule.TargetRef, input.Order.Items)
		if err != nil {
			return "", err
		}
		if rule.Inclusion == "include" {
			includeFound = true
			includeMatched = includeMatched || matched
		} else if rule.Inclusion == "exclude" && matched {
			return "applicability_excluded", nil
		}
		var condition struct {
			MinimumOrderAmount *shared.Money `json:"minimumOrderAmount"`
			StackingPolicyRef  string        `json:"stackingPolicyRef"`
		}
		if err := json.Unmarshal(rule.ConditionValue, &condition); err != nil {
			return "", oops.In("coupon_redemption_application").Code("coupon.applicability_condition_invalid").Wrap(err)
		}
		if rule.ConditionType != "all" && rule.ConditionType != "minimum_order_amount" {
			return "", inputError("coupon.applicability_condition_unsupported", "coupon applicability condition is not supported")
		}
		if condition.StackingPolicyRef != "" && condition.StackingPolicyRef != input.StackingPolicyRef {
			return "stacking_policy_mismatch", nil
		}
		if condition.MinimumOrderAmount != nil {
			if condition.MinimumOrderAmount.Currency != total.Currency {
				return "", inputError("coupon.minimum_order_currency_mismatch", "minimum order amount currency does not match the order")
			}
			minimum, err := moneyRat(*condition.MinimumOrderAmount)
			if err != nil {
				return "", err
			}
			actual, _ := moneyRat(total)
			if actual.Cmp(minimum) < 0 {
				return "minimum_order_amount_not_met", nil
			}
		}
	}
	if includeFound && !includeMatched {
		return "applicability_not_matched", nil
	}
	if !includeFound {
		return "applicability_not_effective", nil
	}
	return "", nil
}

func targetMatches(targetType, targetRef string, items []OrderItem) (bool, error) {
	if targetType == "all" && targetRef == "all" {
		return true, nil
	}
	for _, item := range items {
		switch targetType {
		case "product":
			if item.ProductRef.ID == targetRef {
				return true, nil
			}
		case "drop":
			if item.DropRef != nil && item.DropRef.ID == targetRef {
				return true, nil
			}
		case "seller":
			if item.SellerRef.ID == targetRef {
				return true, nil
			}
		case "category":
			if item.CategoryRef != nil && item.CategoryRef.ID == targetRef {
				return true, nil
			}
		default:
			return false, inputError("coupon.applicability_target_unsupported", "coupon applicability target type is not supported")
		}
	}
	return false, nil
}

func calculateDiscount(benefits []campaign.Benefit, order OrderSnapshot, total shared.Money) (shared.Money, shared.Money, error) {
	if len(benefits) == 0 {
		return shared.Money{}, shared.Money{}, inputError("coupon.benefit_missing", "campaign benefit is required")
	}
	subtotal, err := order.subtotal()
	if err != nil {
		return shared.Money{}, shared.Money{}, err
	}
	shipping, _ := moneyRat(order.ShippingFee)
	discount := new(big.Rat)
	for _, benefit := range benefits {
		var amount *big.Rat
		switch benefit.Type {
		case campaign.BenefitFixedAmount:
			if benefit.Amount == nil || benefit.Amount.Currency != total.Currency {
				return shared.Money{}, shared.Money{}, inputError("coupon.benefit_currency_mismatch", "fixed benefit currency does not match the order")
			}
			amount, err = moneyRat(*benefit.Amount)
		case campaign.BenefitPercentage:
			percentage := new(big.Rat)
			if _, ok := percentage.SetString(benefit.Percentage); !ok {
				return shared.Money{}, shared.Money{}, inputError("coupon.benefit_percentage_invalid", "percentage benefit is invalid")
			}
			amount = new(big.Rat).Mul(subtotal, new(big.Rat).Quo(percentage, big.NewRat(100, 1)))
			if benefit.MaxDiscountAmount != nil {
				if benefit.MaxDiscountAmount.Currency != total.Currency {
					return shared.Money{}, shared.Money{}, inputError("coupon.benefit_currency_mismatch", "maximum discount currency does not match the order")
				}
				maximum, maxErr := moneyRat(*benefit.MaxDiscountAmount)
				if maxErr != nil {
					return shared.Money{}, shared.Money{}, maxErr
				}
				if amount.Cmp(maximum) > 0 {
					amount = maximum
				}
			}
		case campaign.BenefitShippingFee:
			amount = new(big.Rat).Set(shipping)
		default:
			return shared.Money{}, shared.Money{}, inputError("coupon.benefit_type_unsupported", "campaign benefit type is not supported")
		}
		if err != nil {
			return shared.Money{}, shared.Money{}, err
		}
		discount.Add(discount, amount)
	}
	totalAmount, _ := moneyRat(total)
	if discount.Cmp(totalAmount) > 0 {
		discount.Set(totalAmount)
	}
	finalAmount := new(big.Rat).Sub(totalAmount, discount)
	discountText, err := decimal(discount)
	if err != nil {
		return shared.Money{}, shared.Money{}, err
	}
	finalText, err := decimal(finalAmount)
	if err != nil {
		return shared.Money{}, shared.Money{}, err
	}
	return shared.Money{Amount: discountText, Currency: total.Currency}, shared.Money{Amount: finalText, Currency: total.Currency}, nil
}

func calculateCostShares(discount shared.Money, funding shared.IssuerAndFunding) ([]domainredemption.CostShare, error) {
	amount, err := moneyRat(discount)
	if err != nil {
		return nil, err
	}
	share := func(kind string, ref *shared.ExternalRef, value *big.Rat) (domainredemption.CostShare, error) {
		text, decimalErr := decimal(value)
		if decimalErr != nil {
			return domainredemption.CostShare{}, decimalErr
		}
		return domainredemption.CostShare{BearerType: kind, BearerRef: ref, Amount: shared.Money{Amount: text, Currency: discount.Currency}}, nil
	}
	switch funding.FunderType {
	case "platform", "compensation":
		result, err := share(funding.FunderType, funding.FunderRef, amount)
		return []domainredemption.CostShare{result}, err
	case "seller":
		if funding.FunderRef == nil {
			return nil, inputError("coupon.cost_bearer_ref_required", "seller funding requires a bearer reference")
		}
		result, err := share("seller", funding.FunderRef, amount)
		return []domainredemption.CostShare{result}, err
	case "joint":
		if funding.FunderRef == nil {
			return nil, inputError("coupon.cost_bearer_ref_required", "joint funding requires a non-platform bearer reference")
		}
		percentage := new(big.Rat)
		if _, ok := percentage.SetString(funding.PlatformSharePercentage); !ok || percentage.Sign() < 0 || percentage.Cmp(big.NewRat(100, 1)) > 0 {
			return nil, inputError("coupon.platform_share_invalid", "joint funding platform share percentage is invalid")
		}
		platformAmount := new(big.Rat).Mul(amount, new(big.Rat).Quo(percentage, big.NewRat(100, 1)))
		otherAmount := new(big.Rat).Sub(amount, platformAmount)
		platformShare, err := share("platform", nil, platformAmount)
		if err != nil {
			return nil, err
		}
		otherShare, err := share("joint", funding.FunderRef, otherAmount)
		if err != nil {
			return nil, err
		}
		return []domainredemption.CostShare{platformShare, otherShare}, nil
	default:
		return nil, inputError("coupon.cost_bearer_unsupported", "campaign funding type is not supported")
	}
}

func validateTransition(input TransitionInput) error {
	if strings.TrimSpace(input.RedemptionID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.ReasonCode) == "" {
		return inputError("coupon.redemption_transition_input_invalid", "redemption id, expected version, and reason code are required")
	}
	if err := input.ResultRef.Validate(); err != nil {
		return err
	}
	if input.ResultSnapshot != nil {
		return input.ResultSnapshot.Validate()
	}
	return nil
}

func validateReplayPayload(payload ReplayPayload) error {
	if payload.ExpectedVersion < 0 {
		return inputError("coupon.replay_payload_invalid", "replay expected version must not be negative")
	}
	switch payload.Operation {
	case recovery.OperationReserve:
		if payload.ReservedUntil == nil || payload.ReservedUntil.IsZero() {
			return inputError("coupon.replay_payload_invalid", "reserve replay requires the original reservation deadline")
		}
	case recovery.OperationConfirm, recovery.OperationRelease, recovery.OperationReclaim:
		if payload.ResultRef == nil || strings.TrimSpace(payload.ReasonCode) == "" {
			return inputError("coupon.replay_payload_invalid", "transition replay requires the original result reference and reason")
		}
		if err := payload.ResultRef.Validate(); err != nil {
			return err
		}
		if payload.ResultSnapshot != nil {
			return payload.ResultSnapshot.Validate()
		}
	default:
		return inputError("coupon.replay_operation_unsupported", "original coupon operation is not replayable")
	}
	return nil
}

func replayOperation(operation recovery.OperationType) (domainredemption.ReplayOperation, error) {
	switch operation {
	case recovery.OperationReserve:
		return domainredemption.ReplayReserve, nil
	case recovery.OperationConfirm:
		return domainredemption.ReplayConfirm, nil
	case recovery.OperationRelease:
		return domainredemption.ReplayRelease, nil
	case recovery.OperationReclaim:
		return domainredemption.ReplayReclaim, nil
	default:
		return "", inputError("coupon.replay_operation_unsupported", "original coupon operation is not replayable")
	}
}

func recoveryResultKind(kind domainredemption.ReplayResultKind) (recovery.ResultKind, error) {
	switch kind {
	case domainredemption.ReplayTransitioned:
		return recovery.ResultTransitioned, nil
	case domainredemption.ReplayAlreadyApplied:
		return recovery.ResultAlreadyApplied, nil
	case domainredemption.ReplayFailed:
		return recovery.ResultFailed, nil
	default:
		return "", inputError("coupon.replay_result_invalid", "coupon redemption replay returned an unsupported result kind")
	}
}

func newCommand(documentID, operation string, scope []string, request any, metadata Metadata) (reliability.Command, error) {
	if err := validateMetadata(metadata, false); err != nil {
		return reliability.Command{}, err
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return reliability.Command{}, oops.In("coupon_redemption_application").Code("coupon.request_hash_failed").Wrap(err)
	}
	businessKey := metadata.BusinessKey
	if businessKey == "" {
		parts := append(append([]string(nil), scope...), metadata.IdempotencyKey)
		businessKey = strings.Join(parts, "|")
	}
	return reliability.Command{
		DocumentID: documentID, OperationType: operation, BusinessKey: businessKey,
		RequestHash: sha256.Sum256(requestJSON), CorrelationID: metadata.CorrelationID,
		CausationID: metadata.CausationID, TraceID: metadata.TraceID, LeaseUntil: metadata.LeaseUntil, ExpiresAt: metadata.ExpiresAt,
	}, nil
}

func validateMetadata(metadata Metadata, explicitBusinessKey bool) error {
	if metadata.RequestedAt.IsZero() || metadata.LeaseUntil.IsZero() || metadata.ExpiresAt.IsZero() ||
		!metadata.LeaseUntil.After(metadata.RequestedAt) || !metadata.ExpiresAt.After(metadata.LeaseUntil) {
		return inputError("coupon.idempotency_metadata_invalid", "request time, lease deadline, and expiry deadline are required in order")
	}
	if explicitBusinessKey {
		return nil
	}
	if strings.TrimSpace(metadata.BusinessKey) == "" && strings.TrimSpace(metadata.IdempotencyKey) == "" {
		return inputError("coupon.idempotency_key_required", "idempotency key or explicit business key is required")
	}
	return nil
}

func verifyPayloadHash(payload []byte, expected string) ([32]byte, error) {
	digest := sha256.Sum256(payload)
	if !strings.HasPrefix(expected, "sha256:") {
		return [32]byte{}, inputError("coupon.replay_payload_hash_invalid", "replay payload hash must use sha256")
	}
	want := strings.TrimPrefix(expected, "sha256:")
	if want != hex.EncodeToString(digest[:]) && want != base64.RawURLEncoding.EncodeToString(digest[:]) {
		return [32]byte{}, inputError("coupon.replay_payload_hash_mismatch", "immutable replay payload hash does not match")
	}
	return digest, nil
}

func stableID(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(digest[:12])
}

func moneyRat(value shared.Money) (*big.Rat, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	amount := new(big.Rat)
	amount.SetString(value.Amount)
	return amount, nil
}

func decimal(value *big.Rat) (string, error) {
	denominator := new(big.Int).Set(value.Denom())
	two := big.NewInt(2)
	five := big.NewInt(5)
	remainder := new(big.Int)
	twos, fives := 0, 0
	for {
		remainder.Mod(denominator, two)
		if remainder.Sign() != 0 {
			break
		}
		denominator.Quo(denominator, two)
		twos++
	}
	for {
		remainder.Mod(denominator, five)
		if remainder.Sign() != 0 {
			break
		}
		denominator.Quo(denominator, five)
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", inputError("coupon.non_terminating_amount", "calculated coupon amount cannot be represented as a finite decimal")
	}
	scale := twos
	if fives > scale {
		scale = fives
	}
	text := value.FloatString(scale)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" || text == "" {
		return "0", nil
	}
	return text, nil
}

func inputError(code, message string) error {
	return oops.In("coupon_redemption_application").Code(code).New(message)
}
