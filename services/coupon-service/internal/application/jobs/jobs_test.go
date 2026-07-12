package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	operationsapp "github.com/Medikong/services/services/coupon-service/internal/application/operations"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	redemptionapp "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	platformexternal "github.com/Medikong/services/services/coupon-service/internal/platform/external"
	"github.com/samber/oops"
)

func TestBulkIssuePlannerWorkerPersistsCursorAndDeterministicTargets(t *testing.T) {
	now := time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC)
	snapshot := testSnapshot(now)
	store := &bulkStoreFake{leases: []BulkLease{{
		Job: bulk.Job{
			ID: "bjob_12345678", CampaignID: "campaign-1", OwnerServiceID: "operations-service", AudienceDefinitionRef: "audience-1",
			AudienceSnapshot: snapshot, EvaluationAsOf: now.Add(-time.Hour), Status: bulk.StatusRegistered,
			Version: 3, AttemptCount: 0,
		},
		Cursor: "cursor-1", PageNumber: 4, PlannedTargetCount: 8,
	}}}
	repository := &bulkLeaserFake{}
	audience := &audienceFake{page: ports.AudiencePage{
		UserIDs: []string{"user-1", "user-1", "user-2"}, NextCursor: "cursor-2", Snapshot: snapshot,
	}}
	worker, err := NewBulkIssuePlannerWorker("bulk-worker", store, repository, audience, testPolicy(), nil)
	if err != nil {
		t.Fatalf("NewBulkIssuePlannerWorker() error = %v", err)
	}
	worker.now = func() time.Time { return now }

	count, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("RunOnce() count = %d, want 1", count)
	}
	if store.commit.PageNumber != 5 || store.commit.CurrentCursor != "cursor-1" || store.commit.NextCursor != "cursor-2" {
		t.Fatalf("CommitBulkPage() progress = %+v", store.commit)
	}
	if len(store.commit.Targets) != 2 {
		t.Fatalf("CommitBulkPage() target count = %d, want 2", len(store.commit.Targets))
	}
	first := store.commit.Targets[0]
	if first.BusinessKey != "bjob_12345678:user-1" || first.IssueRequestID != "ireq_d128c1c1-283c-5a14-b39d-83ef78112cbb" {
		t.Fatalf("first target = %+v", first)
	}
}

func TestIssueRetryWorkerTransitionsBeforeDurableCommand(t *testing.T) {
	now := time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)
	events := make([]string, 0, 2)
	lease := IssueLease{Request: issuerequest.Request{
		ID: "ireq_12345678", BusinessKey: "bulk:user-1", Status: issuerequest.StatusRetryPending, Version: 7,
	}, WorkerAttempt: 1}
	store := &issueStoreFake{leases: []IssueLease{lease}, events: &events}
	processor := &issueProcessorFake{events: &events}
	worker, err := NewIssueRetryWorker("issue-worker", store, processor, testPolicy(), nil)
	if err != nil {
		t.Fatalf("NewIssueRetryWorker() error = %v", err)
	}
	worker.now = func() time.Time { return now }

	count, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("RunOnce() count = %d, want 1", count)
	}
	if len(events) != 2 || events[0] != "processing" || events[1] != "enqueue" {
		t.Fatalf("transaction order = %v, want [processing enqueue]", events)
	}
	if store.enqueued.Request.Status != issuerequest.StatusProcessing || store.enqueued.Request.Version != 8 {
		t.Fatalf("enqueued request = %+v", store.enqueued.Request)
	}
}

func TestRecoveryWorkerStopsAtReplayAndLeavesResultToPolicy(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	current := recovery.Recovery{
		ID: "rcvy_12345678", RedemptionID: "redm_12345678", OriginalOperationType: recovery.OperationConfirm,
		OriginalPayloadRef: "payload-1", OriginalPayloadHash: "sha256:abc",
		BusinessKey: "order-1", Status: recovery.StatusRetryPending,
		CurrentAttemptID: "att_12345678", Version: 4,
		CurrentAttempt: &recovery.Attempt{ID: "att_12345678", BusinessKey: "order-1", Status: recovery.AttemptRetryPending},
	}
	store := &recoveryStoreFake{leases: []RecoveryLease{{Recovery: current, WorkerAttempt: 1}}}
	leaser := &recoveryLeaserFake{}
	replayer := &replayerFake{}
	worker, err := NewRecoveryWorker("recovery-worker", store, leaser, replayer, testPolicy(), nil)
	if err != nil {
		t.Fatalf("NewRecoveryWorker() error = %v", err)
	}
	worker.now = func() time.Time { return now }

	count, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 1 || replayer.input.RecoveryID != current.ID || replayer.input.AttemptID != current.CurrentAttemptID || replayer.input.RedemptionID != current.RedemptionID {
		t.Fatalf("replay count/input = %d %+v", count, replayer.input)
	}
	if store.failureCalled {
		t.Fatal("successful replay was incorrectly acknowledged through the recovery store")
	}
}

func TestCouponExpiryWorkerUsesStableExpiryTimeAndBacksOffTechnicalFailure(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Hour)
	store := &expiryStoreFake{leases: []ExpiryLease{{
		Coupon: usercoupon.Coupon{ID: "ucpn_1", Version: 2, ExpiresAt: expiresAt}, Attempt: 2,
	}}}
	expirer := &expirerFake{err: errors.New("temporary")}
	classifier := FailureClassifierFunc(func(error) Failure {
		return Failure{Code: "DEPENDENCY_UNAVAILABLE", Retryable: true}
	})
	worker, err := NewCouponExpiryWorker("expiry-worker", store, expirer, testPolicy(), classifier)
	if err != nil {
		t.Fatalf("NewCouponExpiryWorker() error = %v", err)
	}
	worker.now = func() time.Time { return now }

	count, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 1 || expirer.input.AsOf != expiresAt {
		t.Fatalf("expiry count/input = %d %+v", count, expirer.input)
	}
	if store.failure.Terminal || store.failure.Next != now.Add(2*time.Second) {
		t.Fatalf("failure acknowledgement = %+v", store.failure)
	}
}

func TestDefaultFailureClassifierRetriesUnavailableDependenciesAndVersionRaces(t *testing.T) {
	_, dependencyErr := (platformexternal.Unavailable{}).Page(context.Background(), "audience", time.Now(), "", 1)
	failure := (DefaultFailureClassifier{}).Classify(
		oops.In("coupon_bulk_planner_worker").Code("coupon.bulk_audience_failed").Wrap(dependencyErr),
	)
	if !failure.Retryable || failure.Code != "COUPON_WORKER_DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("dependency failure = %+v, want retryable dependency unavailable", failure)
	}

	failure = (DefaultFailureClassifier{}).Classify(
		oops.In("coupon_bulk_repository").Code("coupon.version_conflict").New("bulk job changed concurrently"),
	)
	if !failure.Retryable || failure.Code != "COUPON_WORKER_CONCURRENT_UPDATE" {
		t.Fatalf("version failure = %+v, want retryable concurrent update", failure)
	}
}

func testPolicy() Policy {
	return Policy{
		BatchSize: 10, PageSize: 100, Lease: 20 * time.Second, AttemptTimeout: 5 * time.Second,
		MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: 8 * time.Second,
	}
}

func testSnapshot(now time.Time) shared.SnapshotRef {
	return shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "audience", Type: "definition", ID: "audience-1"},
		SourceVersion: "v1", CapturedAt: now.Add(-time.Hour),
		PayloadHash: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef",
	}
}

type bulkStoreFake struct {
	leases []BulkLease
	commit BulkPageCommit
}

func (f *bulkStoreFake) ClaimBulkJobs(context.Context, string, time.Time, int, time.Duration) ([]BulkLease, error) {
	return f.leases, nil
}
func (f *bulkStoreFake) CommitBulkPage(_ context.Context, input BulkPageCommit) (int64, error) {
	f.commit = input
	return int64(len(input.Targets)), nil
}
func (*bulkStoreFake) FailBulkJob(context.Context, string, string, time.Time, Failure, time.Time, bool) error {
	return nil
}

type bulkLeaserFake struct{}

func (*bulkLeaserFake) Lease(_ context.Context, _ string, _ int64, owner string, until, now time.Time) (bulk.Job, error) {
	return bulk.Job{
		ID: "bjob_12345678", CampaignID: "campaign-1", OwnerServiceID: "operations-service", AudienceDefinitionRef: "audience-1",
		AudienceSnapshot: testSnapshot(now), EvaluationAsOf: now.Add(-time.Hour), Status: bulk.StatusRunning,
		LeaseOwner: owner, LeaseUntil: &until, Version: 4, AttemptCount: 1,
	}, nil
}

type audienceFake struct{ page ports.AudiencePage }

func (f *audienceFake) Page(context.Context, string, time.Time, string, int) (ports.AudiencePage, error) {
	return f.page, nil
}

type issueStoreFake struct {
	leases   []IssueLease
	events   *[]string
	enqueued IssueLease
}

func (f *issueStoreFake) ClaimIssueRequests(context.Context, string, time.Time, int, time.Duration) ([]IssueLease, error) {
	return f.leases, nil
}
func (f *issueStoreFake) EnqueueIssueCommand(_ context.Context, lease IssueLease, _ time.Time) error {
	*f.events = append(*f.events, "enqueue")
	f.enqueued = lease
	return nil
}
func (*issueStoreFake) FailIssueRequest(context.Context, IssueLease, string, time.Time, Failure, time.Time, bool) error {
	return nil
}

type issueProcessorFake struct{ events *[]string }

func (f *issueProcessorFake) MarkProcessing(_ context.Context, id string, version int64, _ issuerequest.Command) (issuerequest.Mutation, error) {
	*f.events = append(*f.events, "processing")
	return issuerequest.Mutation{Request: issuerequest.Request{
		ID: id, BusinessKey: "bulk:user-1", Status: issuerequest.StatusProcessing, Version: version + 1,
	}}, nil
}

type recoveryStoreFake struct {
	leases        []RecoveryLease
	failureCalled bool
}

func (f *recoveryStoreFake) ClaimRecoveries(context.Context, string, time.Time, int, time.Duration) ([]RecoveryLease, error) {
	return f.leases, nil
}
func (f *recoveryStoreFake) FailRecovery(context.Context, RecoveryLease, string, time.Time, Failure, time.Time, bool) error {
	f.failureCalled = true
	return nil
}

type recoveryLeaserFake struct{}

func (*recoveryLeaserFake) Lease(_ context.Context, _ string, _ int64, attemptID, businessKey, owner string, until, now time.Time) (recovery.Recovery, error) {
	return recovery.Recovery{
		ID: "rcvy_12345678", RedemptionID: "redm_12345678", OriginalOperationType: recovery.OperationConfirm,
		OriginalPayloadRef: "payload-1", OriginalPayloadHash: "sha256:abc",
		BusinessKey: businessKey, Status: recovery.StatusRetrying, CurrentAttemptID: attemptID,
		CurrentAttempt: &recovery.Attempt{ID: attemptID, BusinessKey: businessKey, Status: recovery.AttemptRetrying},
		LeaseOwner:     owner, LeaseUntil: &until, Version: 5, UpdatedAt: now,
	}, nil
}

type replayerFake struct{ input redemptionapp.ReplayInput }

func (f *replayerFake) Replay(_ context.Context, input redemptionapp.ReplayInput, _ redemptionapp.Metadata) (redemptionapp.RecoveryResultCommand, error) {
	f.input = input
	return redemptionapp.RecoveryResultCommand{
		RecoveryID: input.RecoveryID, AttemptID: input.AttemptID, BusinessKey: input.BusinessKey,
	}, nil
}

type expiryStoreFake struct {
	leases  []ExpiryLease
	failure struct {
		Next     time.Time
		Terminal bool
	}
}

func (f *expiryStoreFake) ClaimExpirations(context.Context, string, time.Time, int, time.Duration) ([]ExpiryLease, error) {
	return f.leases, nil
}
func (*expiryStoreFake) CompleteExpiration(context.Context, string, string, time.Time) error {
	return nil
}
func (f *expiryStoreFake) FailExpiration(_ context.Context, _ ExpiryLease, _ string, next time.Time, _ Failure, _ time.Time, terminal bool) error {
	f.failure.Next = next
	f.failure.Terminal = terminal
	return nil
}

type expirerFake struct {
	input operationsapp.ExpireUserCouponInput
	err   error
}

func (f *expirerFake) ExpireUserCoupon(_ context.Context, input operationsapp.ExpireUserCouponInput, _ operationsapp.Metadata) (usercoupon.Mutation, error) {
	f.input = input
	return usercoupon.Mutation{}, f.err
}
