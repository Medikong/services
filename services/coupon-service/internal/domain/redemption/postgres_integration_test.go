//go:build integration

package redemption_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
)

func TestRedemptionConsumingGuardAndReplayTransaction(t *testing.T) {
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
	repo := redemption.NewPostgresRepository(pool)
	first := evaluate(t, ctx, repo, "redm_integrate1", "ucpn_integrate1", "order-integrate-1", now)
	_, found, err := repo.FindConsumingByUserCoupon(ctx, first.UserCouponID)
	require.NoError(t, err)
	require.False(t, found)

	reserved, err := repo.Reserve(ctx, first.ID, first.Version, now.Add(time.Minute), command("CMD.A.19-10", "reserve:first", now))
	require.NoError(t, err)
	consuming, found, err := repo.FindConsumingByUserCoupon(ctx, first.UserCouponID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, redemption.StatusReserved, consuming.Status)

	second := evaluate(t, ctx, repo, "redm_integrate2", first.UserCouponID, "order-integrate-2", now.Add(time.Second))
	secondReserveCommand := command("CMD.A.19-10", "reserve:second", now.Add(time.Second))
	_, err = repo.Reserve(ctx, second.ID, second.Version, now.Add(time.Minute), secondReserveCommand)
	require.Error(t, err)
	_, err = repo.Reserve(ctx, second.ID, second.Version, now.Add(time.Minute), secondReserveCommand)
	require.Error(t, err)
	assertCount(t, ctx, pool, `SELECT count(*) FROM coupon_idempotency_records WHERE business_key=$1`, secondReserveCommand.BusinessKey, 0)

	resultRef := shared.ExternalRef{Context: "payment", Type: "confirmation", ID: "payment-integrate-1"}
	replayCommand := command("CMD.A.19-32", "replay:confirm:first", now.Add(2*time.Second))
	outcome, err := repo.Replay(ctx, redemption.ReplayRequest{
		RecoveryID: "rcvy_integrate1", AttemptID: "att_integrate1", BusinessKey: "order:coupon:confirm",
		RedemptionID: reserved.ID, Operation: redemption.ReplayConfirm, ExpectedVersion: reserved.Version,
		ResultRef: &resultRef, ReasonCode: "payment_confirmed", ReplayedAt: now.Add(2 * time.Second),
	}, replayCommand)
	require.NoError(t, err)
	require.Equal(t, redemption.ReplayTransitioned, outcome.ResultKind)
	require.Equal(t, redemption.StatusConfirmed, outcome.Redemption.Status)
	require.Equal(t, resultRef.ID, outcome.ResultRef)
	assertReplayEvent(t, ctx, pool, reserved.ID, "transitioned", resultRef.ID, "")

	replayed, err := repo.Replay(ctx, redemption.ReplayRequest{
		RecoveryID: "rcvy_integrate1", AttemptID: "att_integrate1", BusinessKey: "order:coupon:confirm",
		RedemptionID: reserved.ID, Operation: redemption.ReplayConfirm, ExpectedVersion: reserved.Version,
		ResultRef: &resultRef, ReasonCode: "payment_confirmed", ReplayedAt: now.Add(2 * time.Second),
	}, replayCommand)
	require.NoError(t, err)
	require.Equal(t, outcome.ResultKind, replayed.ResultKind)
	assertCount(t, ctx, pool, `SELECT count(*) FROM domain_outbox WHERE event_document_id='EVT.A.19-41' AND aggregate_id=$1`, reserved.ID, 1)

	alreadyApplied, err := repo.Replay(ctx, redemption.ReplayRequest{
		RecoveryID: "rcvy_integrate3", AttemptID: "att_integrate3", BusinessKey: "order:coupon:confirm:already",
		RedemptionID: reserved.ID, Operation: redemption.ReplayConfirm, ExpectedVersion: reserved.Version,
		ResultRef: &resultRef, ReasonCode: "payment_confirmed", ReplayedAt: now.Add(2500 * time.Millisecond),
	}, command("CMD.A.19-32", "replay:confirm:already", now.Add(2500*time.Millisecond)))
	require.NoError(t, err)
	require.Equal(t, redemption.ReplayAlreadyApplied, alreadyApplied.ResultKind)
	require.Equal(t, resultRef.ID, alreadyApplied.ResultRef)
	assertCount(t, ctx, pool, `SELECT count(*) FROM domain_outbox WHERE event_document_id='EVT.A.19-41' AND aggregate_id=$1`, reserved.ID, 2)

	failedCommand := command("CMD.A.19-32", "replay:confirm:invalid", now.Add(3*time.Second))
	failed, err := repo.Replay(ctx, redemption.ReplayRequest{
		RecoveryID: "rcvy_integrate2", AttemptID: "att_integrate2", BusinessKey: "order:coupon:confirm:invalid",
		RedemptionID: second.ID, Operation: redemption.ReplayConfirm, ExpectedVersion: second.Version,
		ResultRef: &resultRef, ReasonCode: "payment_confirmed", ReplayedAt: now.Add(3 * time.Second),
	}, failedCommand)
	require.NoError(t, err)
	require.Equal(t, redemption.ReplayFailed, failed.ResultKind)
	require.Equal(t, "coupon.redemption_transition_invalid", failed.FailureCode)
	require.Equal(t, redemption.StatusEvaluated, failed.Redemption.Status)
	assertReplayEvent(t, ctx, pool, second.ID, "failed", "", failed.FailureCode)

	reclaimed, err := repo.Reclaim(ctx, outcome.Redemption.ID, outcome.Redemption.Version,
		shared.ExternalRef{Context: "order", Type: "refund", ID: "refund-integrate-1"}, nil, "order_refunded",
		command("CMD.A.19-15", "reclaim:first", now.Add(4*time.Second)))
	require.NoError(t, err)
	require.Equal(t, redemption.StatusReclaimed, reclaimed.Status)
	consuming, found, err = repo.FindConsumingByUserCoupon(ctx, first.UserCouponID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, redemption.StatusReclaimed, consuming.Status)
}

func evaluate(t *testing.T, ctx context.Context, repo redemption.Repository, redemptionID, userCouponID, orderID string, at time.Time) redemption.Redemption {
	t.Helper()
	result, err := repo.Evaluate(ctx, redemption.Evaluation{
		RedemptionID: redemptionID, UserCouponID: userCouponID, CampaignID: "camp_integrate1",
		UserID: "user-integrate-1", OrderID: orderID, BusinessKey: "validate:" + orderID,
		Eligible: true, PolicyVersion: 1, OrderSnapshot: map[string]any{"orderId": orderID},
		OrderSnapshotHash: "sha256:order", Discount: shared.Money{Amount: "1000", Currency: "KRW"},
		FinalOrderAmount: shared.Money{Amount: "9000", Currency: "KRW"},
		CostShares:       []redemption.CostShare{{BearerType: "platform", Amount: shared.Money{Amount: "1000", Currency: "KRW"}}},
		EvaluatedAt:      at,
	}, command("CMD.A.19-09", "validate:"+orderID, at))
	require.NoError(t, err)
	return result
}

func command(documentID, key string, at time.Time) reliability.Command {
	return reliability.Command{
		DocumentID: documentID, OperationType: "coupon.integration." + documentID, BusinessKey: key,
		RequestHash: sha256.Sum256([]byte(key)), CorrelationID: "corr:" + key,
		LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour),
	}
}

func assertReplayEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, redemptionID, kind, resultRef, failureCode string) {
	t.Helper()
	var payload []byte
	err := pool.QueryRow(ctx, `SELECT payload FROM domain_outbox WHERE event_document_id='EVT.A.19-41' AND aggregate_id=$1`, redemptionID).Scan(&payload)
	require.NoError(t, err)
	var event map[string]any
	require.NoError(t, json.Unmarshal(payload, &event))
	require.Equal(t, redemptionID, event["redemption_id"])
	require.Equal(t, kind, event["result_kind"])
	require.Equal(t, resultRef, event["result_ref"])
	require.Equal(t, failureCode, event["failure_code"])
	require.NotEmpty(t, event["recovery_id"])
	require.NotEmpty(t, event["attempt_id"])
	require.NotEmpty(t, event["business_key"])
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query, id string, expected int) {
	t.Helper()
	var actual int
	require.NoError(t, pool.QueryRow(ctx, query, id).Scan(&actual))
	require.Equal(t, expected, actual)
}
