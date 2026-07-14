package redemption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/require"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	domainoperations "github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	domainredemption "github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

func TestValidateCalculatesDiscountAndVerifiesReferences(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	redemptions := &fakeRedemptions{}
	users := &fakeUserCoupons{coupon: testUserCoupon(now)}
	campaigns := &fakeCampaigns{campaign: testCampaign(now)}
	controls := &fakeControls{}
	eligibility := &fakeEligibility{result: ports.UserEligibility{Eligible: true, Snapshot: testSnapshot("identity", "user_group", "group_12345678", now)}}
	products := &fakeProducts{}
	drops := &fakeDrops{}
	sellers := &fakeSellers{}
	orders := &fakeOrders{}
	payments := &fakePayments{}
	cases := &fakeCases{}
	service := newTestService(t, now, 7*time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: users, Campaigns: campaigns, Controls: controls,
		Users: eligibility, Products: products, Drops: drops, Sellers: sellers, Orders: orders,
		Payments: payments, Cases: cases, ReplayPayloads: &fakePayloads{},
	})

	result, err := service.Validate(context.Background(), testValidateInput(now), testMetadata(now))
	require.NoError(t, err)
	require.Equal(t, domainredemption.StatusEvaluated, result.Status)
	require.Equal(t, "1000", result.Discount.Amount)
	require.Equal(t, "3500", result.FinalOrderAmount.Amount)
	require.Len(t, result.CostShares, 2)
	require.Equal(t, "250", result.CostShares[0].Amount.Amount)
	require.Equal(t, "750", result.CostShares[1].Amount.Amount)
	require.Equal(t, "CMD.A.19-09", redemptions.command.DocumentID)
	require.Equal(t, "order_12345678|ucpn_12345678|17|idem_12345678", redemptions.command.BusinessKey)
	require.Equal(t, stableID("redm", redemptions.command.BusinessKey), result.ID)
	require.Equal(t, 1, users.getCalls)
	require.Equal(t, 1, campaigns.getCalls)
	require.Equal(t, 1, redemptions.consumingCalls)
	require.Equal(t, 3, controls.findEffectiveCalls)
	require.Equal(t, 1, eligibility.calls)
	require.Equal(t, 1, products.calls)
	require.Equal(t, 1, drops.calls)
	require.Equal(t, 1, sellers.calls)
	require.Equal(t, 1, orders.calls)
	require.Zero(t, payments.calls)
	require.Zero(t, cases.calls)
}

func TestValidateRecordsStableIneligibleResultWhenCouponIsAlreadyConsuming(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	redemptions := &fakeRedemptions{
		hasConsuming: true,
		consuming: domainredemption.Redemption{
			ID: "redm_existing1", UserCouponID: "ucpn_12345678", Status: domainredemption.StatusReclaimed,
		},
	}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{coupon: testUserCoupon(now)},
		Campaigns: &fakeCampaigns{campaign: testCampaign(now)}, Controls: &fakeControls{},
		Users:    &fakeEligibility{result: ports.UserEligibility{Eligible: true, Snapshot: testSnapshot("identity", "user_group", "group_12345678", now)}},
		Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{},
	})

	result, err := service.Validate(context.Background(), testValidateInput(now), testMetadata(now))
	require.NoError(t, err)
	require.Equal(t, domainredemption.StatusRejected, result.Status)
	require.Equal(t, "user_coupon_already_consuming", result.ReasonCode)
	require.Equal(t, "0", result.Discount.Amount)
	require.Equal(t, 1, redemptions.evaluateCalls)
}

func TestValidateUsesFrozenIssuePolicyWhenCampaignCurrentVersionAdvanced(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	owned := testUserCoupon(now)
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(owned.GrantSnapshot, &snapshot))
	applicability := snapshot["applicability"].(map[string]any)
	applicability["includeTargets"] = []any{}
	owned.GrantSnapshot, _ = json.Marshal(snapshot)
	current := testCampaign(now)
	current.CurrentPolicyVersion = 2
	current.Benefits = []campaign.Benefit{{
		ID: "benefit-v2", PolicyVersion: 2, Type: campaign.BenefitFixedAmount,
		Amount: &shared.Money{Amount: "3000", Currency: "KRW"}, Currency: "KRW",
	}}
	current.Applicability = []campaign.ApplicabilityPolicy{{
		ID: "all-v2", PolicyVersion: 2, TargetType: "all", TargetRef: "all", Inclusion: "include",
		ConditionType: "all", ConditionValue: json.RawMessage(`{}`), EffectiveFrom: now.Add(-time.Minute), SnapshotLabel: "v2",
	}}
	redemptions := &fakeRedemptions{}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{coupon: owned}, Campaigns: &fakeCampaigns{campaign: current}, Controls: &fakeControls{},
		Users:    &fakeEligibility{result: ports.UserEligibility{Eligible: true, Snapshot: testSnapshot("identity", "user_group", "group_12345678", now)}},
		Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{},
	})

	result, err := service.Validate(context.Background(), testValidateInput(now), testMetadata(now))
	require.NoError(t, err)
	require.Equal(t, domainredemption.StatusEvaluated, result.Status)
	require.Equal(t, 1, result.PolicyVersion)
	require.Equal(t, "1000", result.Discount.Amount)
}

func TestValidateRejectsHistoricalCouponWithoutFrozenPolicySnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	owned := testUserCoupon(now)
	owned.GrantSnapshot = json.RawMessage(`{}`)
	current := testCampaign(now)
	current.CurrentPolicyVersion = 2
	redemptions := &fakeRedemptions{}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{coupon: owned}, Campaigns: &fakeCampaigns{campaign: current}, Controls: &fakeControls{},
		Users:    &fakeEligibility{result: ports.UserEligibility{Eligible: true, Snapshot: testSnapshot("identity", "user_group", "group_12345678", now)}},
		Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{},
	})

	_, err := service.Validate(context.Background(), testValidateInput(now), testMetadata(now))
	require.Error(t, err)
	require.Zero(t, redemptions.evaluateCalls)
}

func TestValidateRequiresStackingPolicyBeforeCallingDependencies(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	redemptions := &fakeRedemptions{}
	users := &fakeUserCoupons{coupon: testUserCoupon(now)}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: users, Campaigns: &fakeCampaigns{campaign: testCampaign(now)}, Controls: &fakeControls{},
		Users: &fakeEligibility{}, Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{},
	})
	input := testValidateInput(now)
	input.StackingPolicyRef = ""

	_, err := service.Validate(context.Background(), input, testMetadata(now))
	require.Error(t, err)
	require.Zero(t, redemptions.evaluateCalls)
	require.Zero(t, users.getCalls)
}

func TestValidatePropagatesSnapshotFailureWithoutSavingIneligibleResult(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	redemptions := &fakeRedemptions{}
	orders := &fakeOrders{err: oops.In("test").Code("test.order_unavailable").New("order service unavailable")}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{}, Campaigns: &fakeCampaigns{}, Controls: &fakeControls{},
		Users: &fakeEligibility{}, Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: orders,
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{},
	})

	_, err := service.Validate(context.Background(), testValidateInput(now), testMetadata(now))
	require.Error(t, err)
	require.Zero(t, redemptions.evaluateCalls)
}

func TestReserveUsesConfiguredTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	redemptions := &fakeRedemptions{current: domainredemption.Redemption{ID: "redm_12345678", CampaignID: "camp_12345678", Status: domainredemption.StatusEvaluated}}
	service := newTestService(t, now, 7*time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{}, Campaigns: &fakeCampaigns{}, Controls: &fakeControls{},
		Users: &fakeEligibility{}, Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{},
	})

	_, err := service.Reserve(context.Background(), ReserveInput{RedemptionID: "redm_12345678", ExpectedVersion: 0}, testMetadata(now))
	require.NoError(t, err)
	require.Equal(t, now.Add(7*time.Minute), redemptions.reservedUntil)
	require.Equal(t, "CMD.A.19-10", redemptions.command.DocumentID)
	require.Equal(t, 1, redemptions.reserveCalls)
	require.Zero(t, redemptions.confirmCalls)
	require.Zero(t, redemptions.releaseCalls)
	require.Zero(t, redemptions.reclaimCalls)
}

func TestReplayVerifiesImmutablePayloadAndReturnsCMD33Input(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	resultRef := shared.ExternalRef{Context: "payment", Type: "authorization", ID: "pay_12345678"}
	payload := ReplayPayload{
		Operation: recovery.OperationConfirm, BusinessKey: "order:coupon:confirm", RedemptionID: "redm_12345678",
		ExpectedVersion: 3, ResultRef: &resultRef, ReasonCode: "payment_confirmed",
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	redemptions := &fakeRedemptions{current: domainredemption.Redemption{ID: "redm_12345678", OrderID: "order_12345678"}, replayOutcome: domainredemption.ReplayOutcome{
		Redemption: domainredemption.Redemption{ID: "redm_12345678", Status: domainredemption.StatusConfirmed, ResultRef: resultRef},
		ResultKind: domainredemption.ReplayTransitioned, ResultRef: resultRef.ID,
	}}
	users := &fakeUserCoupons{}
	campaigns := &fakeCampaigns{}
	payments := &fakePayments{}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: users, Campaigns: campaigns, Controls: &fakeControls{},
		Users: &fakeEligibility{}, Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: payments, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{payload: raw},
	})
	metadata := testMetadata(now)
	metadata.BusinessKey = "worker:attempt"

	result, err := service.Replay(context.Background(), ReplayInput{
		RecoveryID: "rcvy_12345678", AttemptID: "att_12345678", BusinessKey: payload.BusinessKey, RedemptionID: payload.RedemptionID,
		OriginalOperationType: recovery.OperationConfirm, OriginalPayloadRef: "payloads/original-1",
		OriginalPayloadHash: "sha256:" + hex.EncodeToString(digest[:]),
	}, metadata)
	require.NoError(t, err)
	require.Equal(t, recovery.ResultTransitioned, result.Kind)
	require.Equal(t, "pay_12345678", result.ResultRef)
	require.Equal(t, payload.BusinessKey, result.BusinessKey)
	require.Equal(t, 1, redemptions.replayCalls)
	require.Zero(t, redemptions.reserveCalls)
	require.Zero(t, redemptions.releaseCalls)
	require.Zero(t, redemptions.reclaimCalls)
	require.Zero(t, users.getCalls)
	require.Zero(t, campaigns.getCalls)
	require.Equal(t, 1, payments.calls)
	require.Equal(t, ports.PaymentResultBinding{RedemptionID: "redm_12345678", OrderID: "order_12345678"}, payments.binding)
	require.Equal(t, digest, redemptions.command.RequestHash)
	require.Equal(t, "CMD.A.19-32", redemptions.command.DocumentID)
	require.Equal(t, "rcvy_12345678|att_12345678|order:coupon:confirm", redemptions.command.BusinessKey)
	require.Equal(t, domainredemption.ReplayConfirm, redemptions.replayRequest.Operation)
}

func TestReplayRejectsHashMismatchBeforeMutation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	redemptions := &fakeRedemptions{}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{}, Campaigns: &fakeCampaigns{}, Controls: &fakeControls{},
		Users: &fakeEligibility{}, Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{payload: []byte(`{"operation":"confirm"}`)},
	})

	result, err := service.Replay(context.Background(), ReplayInput{
		RecoveryID: "rcvy_12345678", AttemptID: "att_12345678", BusinessKey: "business-1", RedemptionID: "redm_12345678",
		OriginalOperationType: recovery.OperationConfirm, OriginalPayloadRef: "payloads/original-1",
		OriginalPayloadHash: "sha256:wrong",
	}, testMetadata(now))
	require.Error(t, err)
	require.Equal(t, recovery.ResultFailed, result.Kind)
	require.Zero(t, redemptions.findCalls)
	require.Zero(t, redemptions.confirmCalls)
}

func TestReplayAlreadyAppliedDoesNotMutateRedemption(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	resultRef := shared.ExternalRef{Context: "payment", Type: "authorization", ID: "pay_12345678"}
	payload := ReplayPayload{
		Operation: recovery.OperationConfirm, BusinessKey: "order:coupon:confirm", RedemptionID: "redm_12345678",
		ExpectedVersion: 3, ResultRef: &resultRef, ReasonCode: "payment_confirmed",
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	redemptions := &fakeRedemptions{replayOutcome: domainredemption.ReplayOutcome{
		Redemption: domainredemption.Redemption{ID: "redm_12345678", Status: domainredemption.StatusConfirmed, ResultRef: resultRef},
		ResultKind: domainredemption.ReplayAlreadyApplied, ResultRef: resultRef.ID,
	}}
	service := newTestService(t, now, time.Minute, Dependencies{
		Redemptions: redemptions, UserCoupons: &fakeUserCoupons{}, Campaigns: &fakeCampaigns{}, Controls: &fakeControls{},
		Users: &fakeEligibility{}, Products: &fakeProducts{}, Drops: &fakeDrops{}, Sellers: &fakeSellers{}, Orders: &fakeOrders{},
		Payments: &fakePayments{}, Cases: &fakeCases{}, ReplayPayloads: &fakePayloads{payload: raw},
	})

	result, err := service.Replay(context.Background(), ReplayInput{
		RecoveryID: "rcvy_12345678", AttemptID: "att_12345678", BusinessKey: payload.BusinessKey, RedemptionID: payload.RedemptionID,
		OriginalOperationType: recovery.OperationConfirm, OriginalPayloadRef: "payloads/original-1",
		OriginalPayloadHash: "sha256:" + hex.EncodeToString(digest[:]),
	}, testMetadata(now))
	require.NoError(t, err)
	require.Equal(t, recovery.ResultAlreadyApplied, result.Kind)
	require.Equal(t, "pay_12345678", result.ResultRef)
	require.Equal(t, 1, redemptions.replayCalls)
}

func newTestService(t *testing.T, now time.Time, ttl time.Duration, deps Dependencies) *Service {
	t.Helper()
	service, err := NewService(deps, ttl, func() time.Time { return now })
	require.NoError(t, err)
	return service
}

func testMetadata(now time.Time) Metadata {
	return Metadata{
		IdempotencyKey: "idem_12345678", CorrelationID: "corr-1", CausationID: "cause-1",
		RequestedAt: now, LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	}
}

func testValidateInput(now time.Time) ValidateInput {
	drop := shared.ExternalRef{Context: "drop", Type: "drop", ID: "drop_12345678"}
	return ValidateInput{
		UserCouponID: "ucpn_12345678", PolicyVersion: 1, StackingPolicyRef: "stacking-v1", EvaluatedAt: now,
		Order: OrderSnapshot{
			Ref: testSnapshot("order", "order", "order_12345678", now), OrderID: "order_12345678", UserID: "user_12345678",
			Items: []OrderItem{{
				ProductRef: shared.ExternalRef{Context: "catalog", Type: "product", ID: "product_12345678"},
				DropRef:    &drop, SellerRef: shared.ExternalRef{Context: "catalog", Type: "seller", ID: "seller_12345678"},
				Quantity: 2, UnitPrice: shared.Money{Amount: "2000", Currency: "KRW"},
			}},
			ShippingFee: shared.Money{Amount: "500", Currency: "KRW"},
		},
	}
}

func testUserCoupon(now time.Time) usercoupon.Coupon {
	return usercoupon.Coupon{
		ID: "ucpn_12345678", CampaignID: "camp_12345678", PolicyVersion: 1, UserID: "user_12345678",
		Status: usercoupon.StatusGranted, UsableFrom: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		GrantSnapshot: json.RawMessage(`{
			"benefit":{"type":"fixed_amount","amount":{"amount":"1000","currency":"KRW"}},
			"applicability":{"policySchemaVersion":1,"includeTargets":[{"context":"drop","type":"drop","id":"drop_12345678"}],"excludeTargets":[],"stackingPolicyRef":"stacking-v1"},
			"issuerAndFunding":{"issuerType":"seller","issuerRef":{"context":"catalog","type":"seller","id":"seller_12345678"},"funderType":"joint","funderRef":{"context":"catalog","type":"seller","id":"seller_12345678"},"platformSharePercentage":"25"}
		}`),
	}
}

func testCampaign(now time.Time) campaign.Campaign {
	funder := shared.ExternalRef{Context: "catalog", Type: "seller", ID: "seller_12345678"}
	return campaign.Campaign{
		ID: "camp_12345678", Status: campaign.StatusApproved, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
		CurrentPolicyVersion: 1,
		Benefits: []campaign.Benefit{{
			ID: "benefit-1", PolicyVersion: 1, Type: campaign.BenefitFixedAmount,
			Amount: &shared.Money{Amount: "1000", Currency: "KRW"}, Currency: "KRW",
		}},
		Applicability: []campaign.ApplicabilityPolicy{{
			ID: "policy-1", PolicyVersion: 1, TargetType: "drop", TargetRef: "drop_12345678", Inclusion: "include",
			ConditionType: "all", ConditionValue: json.RawMessage(`{"schemaVersion":1,"stackingPolicyRef":"stacking-v1"}`),
			EffectiveFrom: now.Add(-time.Hour), SnapshotLabel: "v1",
		}},
		IssuerAndFunding: shared.IssuerAndFunding{
			IssuerType: "seller", IssuerRef: funder, FunderType: "joint", FunderRef: &funder, PlatformSharePercentage: "25",
		},
	}
}

func testSnapshot(contextName, refType, id string, now time.Time) shared.SnapshotRef {
	return shared.SnapshotRef{
		SourceRef: shared.ExternalRef{Context: contextName, Type: refType, ID: id}, SourceVersion: "17", CapturedAt: now,
		PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
}

type fakeRedemptions struct {
	current        domainredemption.Redemption
	consuming      domainredemption.Redemption
	hasConsuming   bool
	evaluation     domainredemption.Evaluation
	replayRequest  domainredemption.ReplayRequest
	replayOutcome  domainredemption.ReplayOutcome
	command        reliability.Command
	reservedUntil  time.Time
	findCalls      int
	consumingCalls int
	evaluateCalls  int
	reserveCalls   int
	confirmCalls   int
	releaseCalls   int
	reclaimCalls   int
	replayCalls    int
}

func (f *fakeRedemptions) Find(context.Context, string) (domainredemption.Redemption, error) {
	f.findCalls++
	return f.current, nil
}

func (f *fakeRedemptions) FindConsumingByUserCoupon(context.Context, string) (domainredemption.Redemption, bool, error) {
	f.consumingCalls++
	return f.consuming, f.hasConsuming, nil
}

func (f *fakeRedemptions) Evaluate(_ context.Context, input domainredemption.Evaluation, command reliability.Command) (domainredemption.Redemption, error) {
	f.evaluateCalls++
	f.evaluation = input
	f.command = command
	result, _, err := domainredemption.NewEvaluation(input)
	return result, err
}

func (f *fakeRedemptions) Reserve(_ context.Context, _ string, _ int64, until time.Time, command reliability.Command) (domainredemption.Redemption, error) {
	f.reserveCalls++
	f.reservedUntil = until
	f.command = command
	result := f.current
	result.Status = domainredemption.StatusReserved
	return result, nil
}

func (f *fakeRedemptions) Confirm(_ context.Context, _ string, _ int64, ref shared.ExternalRef, _ any, _ string, command reliability.Command) (domainredemption.Redemption, error) {
	f.confirmCalls++
	f.command = command
	result := f.current
	result.Status = domainredemption.StatusConfirmed
	result.ResultRef = ref
	return result, nil
}

func (f *fakeRedemptions) Release(_ context.Context, _ string, _ int64, ref shared.ExternalRef, _ any, _ string, command reliability.Command) (domainredemption.Redemption, error) {
	f.releaseCalls++
	f.command = command
	result := f.current
	result.Status = domainredemption.StatusReleased
	result.ResultRef = ref
	return result, nil
}

func (f *fakeRedemptions) Reclaim(_ context.Context, _ string, _ int64, ref shared.ExternalRef, _ any, _ string, command reliability.Command) (domainredemption.Redemption, error) {
	f.reclaimCalls++
	f.command = command
	result := f.current
	result.Status = domainredemption.StatusReclaimed
	result.ResultRef = ref
	return result, nil
}

func (f *fakeRedemptions) Replay(_ context.Context, input domainredemption.ReplayRequest, command reliability.Command) (domainredemption.ReplayOutcome, error) {
	f.replayCalls++
	f.replayRequest = input
	f.command = command
	return f.replayOutcome, nil
}

type fakeUserCoupons struct {
	coupon   usercoupon.Coupon
	getCalls int
}

func (f *fakeUserCoupons) Grant(context.Context, usercoupon.Coupon, usercoupon.Command) (usercoupon.Mutation, error) {
	panic("unexpected Grant call")
}
func (f *fakeUserCoupons) Get(context.Context, string) (usercoupon.Coupon, error) {
	f.getCalls++
	return f.coupon, nil
}
func (f *fakeUserCoupons) GetByIssueRequest(context.Context, string) (usercoupon.Coupon, error) {
	panic("unexpected GetByIssueRequest call")
}
func (f *fakeUserCoupons) FindExpirable(context.Context, time.Time, int) ([]usercoupon.Coupon, error) {
	panic("unexpected FindExpirable call")
}
func (f *fakeUserCoupons) Expire(context.Context, string, int64, time.Time, usercoupon.Command) (usercoupon.Mutation, error) {
	panic("unexpected Expire call")
}

type fakeCampaigns struct {
	campaign campaign.Campaign
	getCalls int
}

func (f *fakeCampaigns) Create(context.Context, campaign.Campaign, campaign.Command) (campaign.Mutation, error) {
	panic("unexpected Create call")
}
func (f *fakeCampaigns) Get(context.Context, string) (campaign.Campaign, error) {
	f.getCalls++
	return f.campaign, nil
}
func (f *fakeCampaigns) ConfigureIssuance(context.Context, string, int64, campaign.QuantityLimit, campaign.Command) (campaign.Mutation, error) {
	panic("unexpected ConfigureIssuance call")
}
func (f *fakeCampaigns) Review(context.Context, string, int64, campaign.Status, string, campaign.Command) (campaign.Mutation, error) {
	panic("unexpected Review call")
}
func (f *fakeCampaigns) AddPolicyVersion(context.Context, string, int64, campaign.PolicyVersion, campaign.Command) (campaign.Mutation, error) {
	panic("unexpected AddPolicyVersion call")
}
func (f *fakeCampaigns) ReserveQuantity(context.Context, string, string, int64, int64, time.Time, campaign.Command) (campaign.QuantityMutation, error) {
	panic("unexpected ReserveQuantity call")
}
func (f *fakeCampaigns) ConfirmQuantity(context.Context, string, string, int64, campaign.Command) (campaign.QuantityMutation, error) {
	panic("unexpected ConfirmQuantity call")
}
func (f *fakeCampaigns) ReleaseQuantity(context.Context, string, string, int64, campaign.Command) (campaign.QuantityMutation, error) {
	panic("unexpected ReleaseQuantity call")
}

type fakeControls struct {
	findEffectiveCalls int
	controls           []domainoperations.Control
}

func (f *fakeControls) Create(context.Context, domainoperations.Control, reliability.Event, reliability.Command) (domainoperations.Control, error) {
	panic("unexpected Create call")
}
func (f *fakeControls) Find(context.Context, string) (domainoperations.Control, error) {
	panic("unexpected Find call")
}
func (f *fakeControls) FindEffective(context.Context, domainoperations.Scope, time.Time) ([]domainoperations.Control, error) {
	f.findEffectiveCalls++
	return f.controls, nil
}
func (f *fakeControls) ApplyNotice(context.Context, string, domainoperations.NoticeUpdate, reliability.Command) (domainoperations.Control, error) {
	panic("unexpected ApplyNotice call")
}

type fakeEligibility struct {
	result ports.UserEligibility
	err    error
	calls  int
}

func (f *fakeEligibility) Snapshot(context.Context, string, time.Time) (ports.UserEligibility, error) {
	f.calls++
	return f.result, f.err
}

type fakeProducts struct{ calls int }

func (f *fakeProducts) VerifyProduct(context.Context, shared.ExternalRef, shared.SnapshotRef) error {
	f.calls++
	return nil
}

type fakeDrops struct{ calls int }

func (f *fakeDrops) VerifyDrop(context.Context, shared.ExternalRef, shared.SnapshotRef) error {
	f.calls++
	return nil
}

type fakeSellers struct{ calls int }

func (f *fakeSellers) VerifySellerOwnership(context.Context, shared.SnapshotRef) error {
	f.calls++
	return nil
}

type fakeOrders struct {
	err   error
	calls int
}

func (f *fakeOrders) VerifyOrder(context.Context, shared.SnapshotRef) error {
	f.calls++
	return f.err
}

type fakePayments struct {
	calls   int
	binding ports.PaymentResultBinding
}

func (f *fakePayments) VerifyPaymentResult(_ context.Context, _ shared.ExternalRef, _ *shared.SnapshotRef, binding ports.PaymentResultBinding) error {
	f.calls++
	f.binding = binding
	return nil
}

type fakeCases struct{ calls int }

func (f *fakeCases) VerifyCase(context.Context, string, ports.CSCaseBinding) error {
	f.calls++
	return nil
}

type fakePayloads struct {
	payload []byte
	err     error
}

func (f *fakePayloads) Load(context.Context, string) ([]byte, error) {
	return append([]byte(nil), f.payload...), f.err
}
