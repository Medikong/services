//go:build integration

package bulk_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestOperationsAggregatesPersistAtomically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_service"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, migration.Migrate(ctx, pool))

	now := time.Now().UTC().Truncate(time.Microsecond)
	testBulk(t, ctx, pool, now)
	testOperationalControl(t, ctx, pool, now)
	testRecovery(t, ctx, pool, now)
}

func testBulk(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	t.Helper()
	job, domainEvent, err := bulk.Register(bulk.Registration{
		JobID: "bjob_integrate1", CampaignID: "camp_integrate1", OwnerServiceID: "operations-service",
		AudienceSnapshot: shared.SnapshotRef{
			SourceRef:     shared.ExternalRef{Context: "audience", Type: "definition", ID: "segment-1"},
			SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
		EvaluationAsOf: now, OperationRequestRef: "task-bulk", ApprovalRef: "approval-bulk", CreatedAt: now,
	})
	require.NoError(t, err)
	job.PlanningComplete = true
	repo := bulk.NewPostgresRepository(pool)
	created, err := repo.Create(ctx, job, domainEvent, command("CMD.A.19-08", "bulk:create", now))
	require.NoError(t, err)
	require.Equal(t, "operations-service", created.OwnerServiceID)
	leased, err := repo.Lease(ctx, created.ID, created.Version, "bulk-worker", now.Add(time.Minute), now)
	require.NoError(t, err)
	require.Equal(t, "operations-service", leased.OwnerServiceID)
	target := int64(2)
	progress, err := repo.AggregateResult(ctx, leased.ID, leased.Version,
		bulk.ResultDelta{TargetCount: &target, SucceededCount: 1, ResultRef: "issue-result-1", RecordedAt: now.Add(time.Second)},
		command("CMD.A.19-18", "bulk:result:1", now.Add(time.Second)))
	require.NoError(t, err)
	completed, err := repo.AggregateResult(ctx, progress.ID, progress.Version,
		bulk.ResultDelta{RejectedCount: 1, ResultRef: "issue-result-2", RecordedAt: now.Add(2 * time.Second)},
		command("CMD.A.19-18", "bulk:result:2", now.Add(2*time.Second)))
	require.NoError(t, err)
	require.Equal(t, bulk.StatusCompletedWithFailures, completed.Status)
	replayed, err := repo.AggregateResult(ctx, progress.ID, progress.Version,
		bulk.ResultDelta{RejectedCount: 1, ResultRef: "issue-result-2", RecordedAt: now.Add(2 * time.Second)},
		command("CMD.A.19-18", "bulk:result:2", now.Add(2*time.Second)))
	require.NoError(t, err)
	require.Equal(t, int64(1), replayed.RejectedCount)
	deduplicated, err := repo.AggregateResult(ctx, completed.ID, completed.Version,
		bulk.ResultDelta{RejectedCount: 1, ResultRef: "issue-result-2", RecordedAt: now.Add(3 * time.Second)},
		command("CMD.A.19-18", "bulk:result:2:duplicate-command", now.Add(3*time.Second)))
	require.NoError(t, err)
	require.Equal(t, int64(1), deduplicated.RejectedCount)
	assertCount(t, ctx, pool, "bulk_coupon_issue_ledger", 4)
}

func testOperationalControl(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	t.Helper()
	control, domainEvent, err := operations.ApplyStop(operations.Stop{
		ControlID: "ctrl_integrate1", Scopes: []operations.Scope{{Type: operations.ScopeCampaign, Ref: "camp_integrate1"}},
		Active: true, EffectiveFrom: now, BlockIssuance: true, OperationRequestRef: "task-stop",
		ApprovalRef: "approval-stop", ReasonCode: "incident", AppliedAt: now,
	})
	require.NoError(t, err)
	repo := operations.NewPostgresRepository(pool)
	created, err := repo.Create(ctx, control, domainEvent, command("CMD.A.19-20", "control:create", now))
	require.NoError(t, err)
	updated, err := repo.ApplyNotice(ctx, created.ID, operations.NoticeUpdate{
		ExpectedVersion: created.Version, Message: "Coupon issuance is temporarily unavailable.",
		EffectiveFrom: now.Add(time.Minute), Active: true, AppliedAt: now.Add(time.Second),
	}, command("CMD.A.19-31", "control:notice", now.Add(time.Second)))
	require.NoError(t, err)
	require.True(t, updated.Active)
	require.True(t, updated.BlockIssuance)
	require.True(t, updated.Notice.Active)
	effective, err := repo.FindEffective(ctx, operations.Scope{Type: operations.ScopeCampaign, Ref: "camp_integrate1"}, now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, effective, 1)
	assertCount(t, ctx, pool, "coupon_operation_ledger", 2)
}

func testRecovery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	t.Helper()
	value, domainEvent, err := recovery.RecordFailure(recovery.Failure{
		RecoveryID: "rcvy_integrate1", RedemptionID: "redm_integrate1", OriginalOperationType: recovery.OperationConfirm,
		OriginalPayloadRef: "payload-1", OriginalPayloadHash: "sha256:payload-1",
		BusinessKey: "order-1:coupon-1:confirm", FailureCode: "upstream_timeout", OccurredAt: now,
	})
	require.NoError(t, err)
	repo := recovery.NewPostgresRepository(pool)
	created, err := repo.RecordFailure(ctx, value, domainEvent, command("CMD.A.19-34", "recovery:create", now))
	require.NoError(t, err)
	retried, err := repo.RequestRetry(ctx, created.ID, recovery.RetryRequest{
		ExpectedVersion: created.Version, AttemptID: "att_integrate1", NextAttemptAt: now,
		ReasonCode: "manual_retry", OperationRequestRef: "task-retry", ApprovalRef: "approval-retry", RequestedAt: now.Add(time.Second),
	}, command("CMD.A.19-21", "recovery:retry", now.Add(time.Second)))
	require.NoError(t, err)
	leased, err := repo.Lease(ctx, retried.ID, retried.Version, retried.CurrentAttemptID, retried.BusinessKey,
		"recovery-worker", now.Add(time.Minute), now.Add(2*time.Second))
	require.NoError(t, err)
	completed, err := repo.RecordResult(ctx, leased.ID, recovery.ReplayResult{
		ExpectedVersion: leased.Version, AttemptID: leased.CurrentAttemptID, BusinessKey: leased.BusinessKey,
		Kind: recovery.ResultAlreadyApplied, ResultRef: "redemption-result-1", RecordedAt: now.Add(3 * time.Second),
	}, command("CMD.A.19-33", "recovery:result", now.Add(3*time.Second)))
	require.NoError(t, err)
	require.Equal(t, recovery.StatusCompleted, completed.Status)
	require.Equal(t, "redemption-result-1", completed.ResultRef)
	assertCount(t, ctx, pool, "coupon_recovery_ledger", 4)
	assertCount(t, ctx, pool, "coupon_recovery_attempts", 1)
}

func command(documentID, key string, at time.Time) reliability.Command {
	return reliability.Command{
		DocumentID: documentID, OperationType: documentID, BusinessKey: key,
		RequestHash: sha256.Sum256([]byte(key)), CorrelationID: "corr:" + key,
		LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour),
	}
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, expected int) {
	t.Helper()
	var actual int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&actual))
	require.Equal(t, expected, actual, table)
}
