package operations

import (
	"context"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/require"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	domainoperations "github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	domainredemption "github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

func TestCommandHandlersMutateOnlyTheirTargetAggregate(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 5, 6, 0, time.UTC)

	t.Run("CMD08 registers only bulk job", func(t *testing.T) {
		fixture := newFixture(t, now)
		result, err := fixture.service.RegisterBulkJob(context.Background(), RegisterBulkJobInput{
			CampaignID: "camp_12345678", OwnerServiceID: "operations-service", AudienceSnapshot: fixture.audience.page.Snapshot,
			EvaluationAsOf: now, OperationTaskRef: operationTask(), ApprovalRef: "approval-1",
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, stableID("bjob", fixture.bulkJobs.command.BusinessKey), result.ID)
		require.Equal(t, "operations-service", result.OwnerServiceID)
		require.Equal(t, "CMD.A.19-08", fixture.bulkJobs.command.DocumentID)
		require.Equal(t, 1, fixture.audience.calls)
		require.Equal(t, 1, fixture.approvals.calls)
		fixture.requireMutations(t, 1, 0, 0, 0)
	})

	t.Run("CMD18 aggregates only bulk job", func(t *testing.T) {
		fixture := newFixture(t, now)
		target := int64(2)
		_, err := fixture.service.AggregateBulkResult(context.Background(), AggregateBulkResultInput{
			BulkJobID: "bjob_12345678", ExpectedVersion: 2, TargetCount: &target,
			SucceededCount: 2, ResultRef: "event-1", Final: true, RecordedAt: now,
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, "CMD.A.19-18", fixture.bulkJobs.command.DocumentID)
		require.Equal(t, int64(2), *fixture.bulkJobs.delta.TargetCount)
		fixture.requireMutations(t, 1, 0, 0, 0)
	})

	t.Run("CMD20 applies only operational control", func(t *testing.T) {
		fixture := newFixture(t, now)
		result, err := fixture.service.ApplyOperationalStop(context.Background(), ApplyOperationalStopInput{
			Scopes: []domainoperations.Scope{{Type: domainoperations.ScopeCampaign, Ref: "camp_12345678"}},
			Active: true, EffectiveFrom: now, BlockIssuance: true, BlockRedemption: true,
			OperationTaskRef: operationTask(), ApprovalRef: "approval-1", ReasonCode: "incident",
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, stableID("ctrl", fixture.controls.command.BusinessKey), result.ID)
		require.Equal(t, "CMD.A.19-20", fixture.controls.command.DocumentID)
		fixture.requireMutations(t, 0, 1, 0, 0)
	})

	t.Run("CMD21 requests only recovery", func(t *testing.T) {
		fixture := newFixture(t, now)
		result, err := fixture.service.RequestRecovery(context.Background(), RequestRecoveryInput{
			RecoveryID: "rcvy_12345678", ReasonCode: "retry", NextAttemptAt: now.Add(time.Minute),
			OperationTaskRef: operationTask(), ApprovalRef: "approval-1",
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, fixture.recoveries.current.ID, result.ID)
		require.Equal(t, fixture.recoveries.current.Version, fixture.recoveries.retry.ExpectedVersion)
		require.Equal(t, stableID("att", fixture.recoveries.command.BusinessKey), fixture.recoveries.retry.AttemptID)
		require.Equal(t, "CMD.A.19-21", fixture.recoveries.command.DocumentID)
		fixture.requireMutations(t, 0, 0, 1, 0)
	})

	t.Run("CMD24 expires only user coupon", func(t *testing.T) {
		fixture := newFixture(t, now)
		_, err := fixture.service.ExpireUserCoupon(context.Background(), ExpireUserCouponInput{
			UserCouponID: "ucpn_12345678", ExpectedVersion: 4, AsOf: now,
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, "coupon.user_coupon.expire", fixture.userCoupons.command.OperationType)
		require.NotEmpty(t, fixture.userCoupons.command.RequestHash)
		fixture.requireMutations(t, 0, 0, 0, 1)
	})

	t.Run("CMD25 finalizes only recovery", func(t *testing.T) {
		fixture := newFixture(t, now)
		_, err := fixture.service.FinalizeRecovery(context.Background(), FinalizeRecoveryInput{
			RecoveryID: "rcvy_12345678", ReasonCode: "approved_final",
			OperationTaskRef: operationTask(), ApprovalRef: "approval-1",
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, fixture.recoveries.current.Version, fixture.recoveries.finalization.ExpectedVersion)
		require.Equal(t, "CMD.A.19-25", fixture.recoveries.command.DocumentID)
		fixture.requireMutations(t, 0, 0, 1, 0)
	})

	t.Run("CMD31 updates only operational control notice", func(t *testing.T) {
		fixture := newFixture(t, now)
		_, err := fixture.service.ApplyReadOnlyNotice(context.Background(), ApplyReadOnlyNoticeInput{
			ControlID: "ctrl_12345678", ExpectedVersion: 3, Message: "쿠폰 사용이 잠시 중단됩니다.",
			EffectiveFrom: now, Active: true, OperationTaskRef: operationTask(), ApprovalRef: "approval-1",
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, int64(3), fixture.controls.notice.ExpectedVersion)
		require.Equal(t, "CMD.A.19-31", fixture.controls.command.DocumentID)
		fixture.requireMutations(t, 0, 1, 0, 0)
	})

	t.Run("CMD33 records only correlated recovery result", func(t *testing.T) {
		fixture := newFixture(t, now)
		_, err := fixture.service.RecordRecoveryResult(context.Background(), RecordRecoveryResultInput{
			RecoveryID: fixture.recoveries.current.ID,
			AttemptID:  fixture.recoveries.current.CurrentAttemptID, BusinessKey: fixture.recoveries.current.BusinessKey,
			Kind: recovery.ResultTransitioned, ResultRef: "result-1", RecordedAt: now,
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, "CMD.A.19-33", fixture.recoveries.command.DocumentID)
		require.Equal(t, fixture.recoveries.current.CurrentAttemptID, fixture.recoveries.result.AttemptID)
		fixture.requireMutations(t, 0, 0, 1, 0)
	})

	t.Run("CMD34 records only recovery failure", func(t *testing.T) {
		fixture := newFixture(t, now)
		result, err := fixture.service.RecordProcessingFailure(context.Background(), RecordProcessingFailureInput{
			RedemptionID: "redm_12345678", OriginalOperationType: recovery.OperationConfirm, OriginalPayloadRef: "payloads/original-1",
			OriginalPayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			BusinessKey:         "order:coupon:confirm", FailureCode: "postgres_timeout", OccurredAt: now,
		}, metadata(now))
		require.NoError(t, err)
		require.Equal(t, stableID("rcvy", fixture.recoveries.command.BusinessKey), result.ID)
		require.Equal(t, "CMD.A.19-34", fixture.recoveries.command.DocumentID)
		require.Equal(t, "payloads/original-1", fixture.recoveries.failure.OriginalPayloadRef)
		fixture.requireMutations(t, 0, 0, 1, 0)
	})
}

func TestApprovalFailureIsNotConvertedToSuccessAndDoesNotMutate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 4, 5, 6, 0, time.UTC)
	fixture := newFixture(t, now)
	fixture.approvals.err = oops.In("test").Code("test.approval_unavailable").New("approval service unavailable")

	_, err := fixture.service.ApplyOperationalStop(context.Background(), ApplyOperationalStopInput{
		Scopes: []domainoperations.Scope{{Type: domainoperations.ScopeCampaign, Ref: "camp_12345678"}},
		Active: true, EffectiveFrom: now, BlockRedemption: true,
		OperationTaskRef: operationTask(), ApprovalRef: "approval-1",
	}, metadata(now))
	require.Error(t, err)
	fixture.requireMutations(t, 0, 0, 0, 0)
}

func TestRecoveryResultRejectsStaleCorrelationBeforeMutation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 4, 5, 6, 0, time.UTC)
	fixture := newFixture(t, now)

	_, err := fixture.service.RecordRecoveryResult(context.Background(), RecordRecoveryResultInput{
		RecoveryID: fixture.recoveries.current.ID,
		AttemptID:  "att_stale123", BusinessKey: fixture.recoveries.current.BusinessKey,
		Kind: recovery.ResultTransitioned, ResultRef: "result-1", RecordedAt: now,
	}, metadata(now))
	require.Error(t, err)
	fixture.requireMutations(t, 0, 0, 0, 0)
}

func TestExpirationFailsClosedForEveryConsumingRedemptionStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 4, 5, 6, 0, time.UTC)
	for _, status := range []domainredemption.Status{
		domainredemption.StatusReserved,
		domainredemption.StatusConfirmed,
		domainredemption.StatusReclaimed,
	} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			fixture := newFixture(t, now)
			fixture.redemptions.hasConsuming = true
			fixture.redemptions.current = domainredemption.Redemption{
				ID: "redm_consuming1", UserCouponID: "ucpn_12345678", Status: status,
			}

			_, err := fixture.service.ExpireUserCoupon(context.Background(), ExpireUserCouponInput{
				UserCouponID: "ucpn_12345678", ExpectedVersion: 4, AsOf: now,
			}, metadata(now))
			require.Error(t, err)
			require.Equal(t, 1, fixture.redemptions.calls)
			fixture.requireMutations(t, 0, 0, 0, 0)
		})
	}
}

type fixture struct {
	service     *Service
	bulkJobs    *fakeBulkJobs
	controls    *fakeControls
	recoveries  *fakeRecoveries
	userCoupons *fakeUserCoupons
	redemptions *fakeConsumingRedemptions
	audience    *fakeAudience
	approvals   *fakeApprovals
	cases       *fakeCases
}

func newFixture(t *testing.T, now time.Time) *fixture {
	t.Helper()
	audience := &fakeAudience{page: ports.AudiencePage{Snapshot: snapshot(now)}}
	attempt := &recovery.Attempt{
		RecoveryID: "rcvy_12345678", ID: "att_12345678", BusinessKey: "order:coupon:confirm",
		Status: recovery.AttemptRetrying, CreatedAt: now.Add(-time.Minute),
	}
	recoveries := &fakeRecoveries{current: recovery.Recovery{
		ID: "rcvy_12345678", RedemptionID: "redm_12345678", OriginalOperationType: recovery.OperationConfirm,
		OriginalPayloadRef: "payloads/original-1", OriginalPayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		BusinessKey: "order:coupon:confirm", Status: recovery.StatusRetrying,
		CurrentAttemptID: attempt.ID, CurrentAttempt: attempt, Version: 7,
	}}
	result := &fixture{
		bulkJobs: &fakeBulkJobs{}, controls: &fakeControls{}, recoveries: recoveries,
		userCoupons: &fakeUserCoupons{}, redemptions: &fakeConsumingRedemptions{},
		audience: audience, approvals: &fakeApprovals{}, cases: &fakeCases{},
	}
	service, err := NewService(Dependencies{
		BulkJobs: result.bulkJobs, Controls: result.controls, Recoveries: result.recoveries,
		UserCoupons: result.userCoupons, Redemptions: result.redemptions,
		Audience: result.audience, Approvals: result.approvals, Cases: result.cases,
	})
	require.NoError(t, err)
	result.service = service
	return result
}

func (f *fixture) requireMutations(t *testing.T, bulkCount, controlCount, recoveryCount, userCouponCount int) {
	t.Helper()
	require.Equal(t, bulkCount, f.bulkJobs.createCalls+f.bulkJobs.aggregateCalls+f.bulkJobs.leaseCalls)
	require.Equal(t, controlCount, f.controls.createCalls+f.controls.noticeCalls)
	require.Equal(t, recoveryCount, f.recoveries.recordFailureCalls+f.recoveries.requestRetryCalls+f.recoveries.recordResultCalls+f.recoveries.finalizeCalls+f.recoveries.leaseCalls)
	require.Equal(t, userCouponCount, f.userCoupons.expireCalls+f.userCoupons.grantCalls)
}

func metadata(now time.Time) Metadata {
	return Metadata{
		IdempotencyKey: "idem_12345678", CorrelationID: "corr-1", CausationID: "cause-1", TraceID: "trace-1",
		RequestedAt: now, LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	}
}

func operationTask() shared.ExternalRef {
	return shared.ExternalRef{Context: "operations", Type: "task", ID: "task_12345678"}
}

func snapshot(now time.Time) shared.SnapshotRef {
	return shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "audience", Type: "definition", ID: "audience_12345678"},
		SourceVersion: "3", CapturedAt: now, PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
}

type fakeBulkJobs struct {
	command        reliability.Command
	delta          bulk.ResultDelta
	createCalls    int
	aggregateCalls int
	leaseCalls     int
}

func (f *fakeBulkJobs) Create(_ context.Context, job bulk.Job, _ reliability.Event, command reliability.Command) (bulk.Job, error) {
	f.createCalls++
	f.command = command
	return job, nil
}
func (f *fakeBulkJobs) Find(context.Context, string) (bulk.Job, error) {
	panic("unexpected Find call")
}
func (f *fakeBulkJobs) FindDue(context.Context, time.Time, int) ([]bulk.Job, error) {
	panic("unexpected FindDue call")
}
func (f *fakeBulkJobs) Lease(context.Context, string, int64, string, time.Time, time.Time) (bulk.Job, error) {
	f.leaseCalls++
	return bulk.Job{}, nil
}
func (f *fakeBulkJobs) AggregateResult(_ context.Context, id string, _ int64, delta bulk.ResultDelta, command reliability.Command) (bulk.Job, error) {
	f.aggregateCalls++
	f.delta = delta
	f.command = command
	return bulk.Job{ID: id}, nil
}

type fakeControls struct {
	command     reliability.Command
	notice      domainoperations.NoticeUpdate
	createCalls int
	noticeCalls int
}

func (f *fakeControls) Create(_ context.Context, control domainoperations.Control, _ reliability.Event, command reliability.Command) (domainoperations.Control, error) {
	f.createCalls++
	f.command = command
	return control, nil
}
func (f *fakeControls) Find(context.Context, string) (domainoperations.Control, error) {
	panic("unexpected Find call")
}
func (f *fakeControls) FindEffective(context.Context, domainoperations.Scope, time.Time) ([]domainoperations.Control, error) {
	panic("unexpected FindEffective call")
}
func (f *fakeControls) ApplyNotice(_ context.Context, id string, input domainoperations.NoticeUpdate, command reliability.Command) (domainoperations.Control, error) {
	f.noticeCalls++
	f.notice = input
	f.command = command
	return domainoperations.Control{ID: id, Version: input.ExpectedVersion + 1}, nil
}

type fakeRecoveries struct {
	current            recovery.Recovery
	command            reliability.Command
	retry              recovery.RetryRequest
	result             recovery.ReplayResult
	finalization       recovery.Finalization
	failure            recovery.Recovery
	recordFailureCalls int
	requestRetryCalls  int
	recordResultCalls  int
	finalizeCalls      int
	leaseCalls         int
}

func (f *fakeRecoveries) RecordFailure(_ context.Context, value recovery.Recovery, _ reliability.Event, command reliability.Command) (recovery.Recovery, error) {
	f.recordFailureCalls++
	f.failure = value
	f.command = command
	return value, nil
}
func (f *fakeRecoveries) Find(context.Context, string) (recovery.Recovery, error) {
	return f.current, nil
}
func (f *fakeRecoveries) FindDue(context.Context, time.Time, int) ([]recovery.Recovery, error) {
	panic("unexpected FindDue call")
}
func (f *fakeRecoveries) RequestRetry(_ context.Context, _ string, input recovery.RetryRequest, command reliability.Command) (recovery.Recovery, error) {
	f.requestRetryCalls++
	f.retry = input
	f.command = command
	return f.current, nil
}
func (f *fakeRecoveries) Lease(context.Context, string, int64, string, string, string, time.Time, time.Time) (recovery.Recovery, error) {
	f.leaseCalls++
	return f.current, nil
}
func (f *fakeRecoveries) RecordResult(_ context.Context, _ string, input recovery.ReplayResult, command reliability.Command) (recovery.Recovery, error) {
	f.recordResultCalls++
	f.result = input
	f.command = command
	return f.current, nil
}
func (f *fakeRecoveries) Finalize(_ context.Context, _ string, input recovery.Finalization, command reliability.Command) (recovery.Recovery, error) {
	f.finalizeCalls++
	f.finalization = input
	f.command = command
	return f.current, nil
}

type fakeUserCoupons struct {
	command     usercoupon.Command
	grantCalls  int
	expireCalls int
}

type fakeConsumingRedemptions struct {
	current      domainredemption.Redemption
	hasConsuming bool
	err          error
	calls        int
}

func (f *fakeConsumingRedemptions) FindConsumingByUserCoupon(context.Context, string) (domainredemption.Redemption, bool, error) {
	f.calls++
	return f.current, f.hasConsuming, f.err
}

func (f *fakeUserCoupons) Grant(context.Context, usercoupon.Coupon, usercoupon.Command) (usercoupon.Mutation, error) {
	f.grantCalls++
	return usercoupon.Mutation{}, nil
}
func (f *fakeUserCoupons) Get(context.Context, string) (usercoupon.Coupon, error) {
	panic("unexpected Get call")
}
func (f *fakeUserCoupons) GetByIssueRequest(context.Context, string) (usercoupon.Coupon, error) {
	panic("unexpected GetByIssueRequest call")
}
func (f *fakeUserCoupons) FindExpirable(context.Context, time.Time, int) ([]usercoupon.Coupon, error) {
	panic("unexpected FindExpirable call")
}
func (f *fakeUserCoupons) Expire(_ context.Context, id string, version int64, _ time.Time, command usercoupon.Command) (usercoupon.Mutation, error) {
	f.expireCalls++
	f.command = command
	return usercoupon.Mutation{Coupon: usercoupon.Coupon{ID: id, Version: version + 1}}, nil
}

type fakeAudience struct {
	page  ports.AudiencePage
	err   error
	calls int
}

func (f *fakeAudience) Page(context.Context, string, time.Time, string, int) (ports.AudiencePage, error) {
	f.calls++
	return f.page, f.err
}

type fakeApprovals struct {
	err   error
	calls int
}

func (f *fakeApprovals) VerifyApproval(context.Context, string, string) error {
	f.calls++
	return f.err
}

type fakeCases struct{ calls int }

func (f *fakeCases) VerifyCase(context.Context, string, ports.CSCaseBinding) error {
	f.calls++
	return nil
}
