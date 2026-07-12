//go:build integration

package projection_test

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/application/projection"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestProjectorAndTypedQueriesUseMigratedReadModels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := projectionPostgres(t, ctx)
	projector, err := projection.New(pool, "coupon-read-model-projector-v1")
	require.NoError(t, err)
	repository, err := readmodel.NewPostgresRepository(pool)
	require.NoError(t, err)

	now := time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)
	accepted := projectionEvent("EVT.A.19-07", "coupon.issue.accepted", "CouponIssueRequest", "ireq_abcdefgh", 0, now, map[string]any{
		"issueRequestId": "ireq_abcdefgh", "campaignId": "camp_abcdefgh", "userId": "user-1",
		"businessKey": "claim:user-1", "sourceRef": "api:claim:1", "status": "accepted",
	})
	require.NoError(t, projector.Handle(ctx, accepted))

	issued := projectionEvent("EVT.A.19-09", "coupon.user_coupon.issued", "UserCoupon", "ucpn_abcdefgh", 0, now.Add(time.Second), map[string]any{
		"userCouponId": "ucpn_abcdefgh", "campaignId": "camp_abcdefgh", "policyVersion": 1,
		"userId": "user-1", "issueRequestId": "ireq_abcdefgh", "status": "granted",
		"usableFrom": now, "expiresAt": now.Add(24 * time.Hour), "resultRef": "user_coupon:ucpn_abcdefgh:granted",
		"grantSnapshot": map[string]any{
			"displayName":   "출시 기념 쿠폰",
			"benefit":       map[string]any{"type": "fixed_amount", "amount": map[string]any{"amount": "1000", "currency": "KRW"}},
			"applicability": map[string]any{"policySchemaVersion": 1, "includeTargets": []any{}, "excludeTargets": []any{}},
			"issuerAndFunding": map[string]any{
				"issuerType": "platform", "issuerRef": map[string]any{"context": "coupon", "type": "platform", "id": "platform"},
				"funderType": "platform",
			},
		},
	})
	require.NoError(t, projector.Handle(ctx, issued))
	require.NoError(t, projector.Handle(ctx, issued), "same event ID and payload must be idempotent")

	wallet, err := repository.ListWallet(ctx, readmodel.WalletQuery{UserID: "user-1", Limit: 1})
	require.NoError(t, err)
	require.Len(t, wallet.Items, 1)
	require.Equal(t, readmodel.WalletStatusAvailable, wallet.Items[0].Status)
	require.Equal(t, "1000", wallet.Items[0].Benefit.Amount.Amount)
	detail, err := repository.GetCouponDetail(ctx, "user-1", "ucpn_abcdefgh")
	require.NoError(t, err)
	require.Equal(t, "출시 기념 쿠폰", detail.Document.DisplayName)
	_, err = repository.GetCouponDetail(ctx, "different-user", "ucpn_abcdefgh")
	require.ErrorIs(t, err, readmodel.ErrNotFound)

	redemptionData := func(status string) map[string]any {
		return map[string]any{
			"redemption_id": "redm_abcdefgh", "user_coupon_id": "ucpn_abcdefgh", "campaign_id": "camp_abcdefgh",
			"user_id": "user-1", "order_ref": map[string]any{"context": "order", "type": "order", "id": "order-1"},
			"status": status, "result_ref": map[string]any{"context": "coupon", "type": "redemption", "id": "redm_abcdefgh"},
			"policy_version": 1, "discount": map[string]any{"amount": "1000", "currency": "KRW"},
			"final_order_amount": map[string]any{"amount": "9000", "currency": "KRW"},
			"cost_shares":        []any{map[string]any{"bearerType": "platform", "amount": map[string]any{"amount": "1000", "currency": "KRW"}}},
			"evaluated_at":       now, "reserved_until": now.Add(10 * time.Minute),
		}
	}
	reserved := projectionEvent("EVT.A.19-21", "coupon.redemption.reserved", "CouponRedemption", "redm_abcdefgh", 1, now.Add(2*time.Second), redemptionData("reserved"))
	confirmed := projectionEvent("EVT.A.19-22", "coupon.redemption.confirmed", "CouponRedemption", "redm_abcdefgh", 2, now.Add(3*time.Second), redemptionData("confirmed"))
	attributed := projectionEvent("EVT.A.19-28", "coupon.cost_attribution.recorded", "CouponRedemption", "redm_abcdefgh", 2, now.Add(3*time.Second), redemptionData("confirmed"))
	require.NoError(t, projector.Handle(ctx, reserved))
	require.NoError(t, projector.Handle(ctx, confirmed))
	require.NoError(t, projector.Handle(ctx, attributed))

	wallet, err = repository.ListWallet(ctx, readmodel.WalletQuery{UserID: "user-1"})
	require.NoError(t, err)
	require.Equal(t, readmodel.WalletStatusUsed, wallet.Items[0].Status)
	performance, err := repository.CampaignPerformance(ctx, readmodel.PerformanceQuery{CampaignID: "camp_abcdefgh"})
	require.NoError(t, err)
	require.Equal(t, readmodel.PerformanceCounts{Requested: 1, Issued: 1, Reserved: 1, Confirmed: 1}, performance.Counts)
	require.Equal(t, &readmodel.Money{Amount: "1000.0000", Currency: "KRW"}, performance.ConfirmedDiscount)

	costs, err := repository.ListCostAttributions(ctx, readmodel.CostAttributionQuery{CampaignID: "camp_abcdefgh", Limit: 1})
	require.NoError(t, err)
	require.Len(t, costs.Items, 1)
	require.Equal(t, readmodel.CostAttributionConfirmed, costs.Items[0].Kind)
	require.Equal(t, "order-1", costs.Items[0].OrderRef.ID)

	failure := projectionEvent("EVT.A.19-10", "coupon.issue.failed_retryable", "CouponIssueRequest", "ireq_failure1", 1, now.Add(4*time.Second), map[string]any{
		"issueRequestId": "ireq_failure1", "campaignId": "camp_abcdefgh", "userId": "user-1",
		"businessKey": "claim:user-1:retry", "sourceRef": "api:claim:retry", "status": "failed_retryable",
		"failureCode": "UPSTREAM_UNAVAILABLE", "retryCount": 1, "nextAttemptAt": now.Add(time.Minute),
	})
	require.NoError(t, projector.Handle(ctx, failure))
	failures, err := repository.ListFailures(ctx, readmodel.FailureQuery{Status: "failed_retryable", Limit: 10})
	require.NoError(t, err)
	require.Len(t, failures.Items, 1)
	require.Equal(t, "UPSTREAM_UNAVAILABLE", failures.Items[0].FailureCode)

	notice := projectionEvent("EVT.A.19-38", "coupon.read_only_notice.applied", "CouponOperationalControl", "ctrl_abcdefgh", 1, now.Add(5*time.Second), map[string]any{
		"control_id": "ctrl_abcdefgh", "scopes": []any{map[string]any{"type": "campaign", "ref": "camp_abcdefgh"}},
		"notice": map[string]any{"message": "쿠폰 처리가 잠시 지연됩니다.", "active": true, "effectiveFrom": now},
	})
	require.NoError(t, projector.Handle(ctx, notice))
	notices, err := repository.ListActiveNotices(ctx, readmodel.NoticeQuery{
		Scopes: []readmodel.NoticeScope{{Type: "campaign", Ref: "camp_abcdefgh"}}, AsOf: now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Equal(t, "쿠폰 처리가 잠시 지연됩니다.", notices[0].Message)

	timeline, err := repository.ListTimeline(ctx, readmodel.TimelineQuery{UserID: "user-1", Limit: 2})
	require.NoError(t, err)
	require.Len(t, timeline.Items, 2)
	require.NotEmpty(t, timeline.NextCursor)
	nextTimeline, err := repository.ListTimeline(ctx, readmodel.TimelineQuery{UserID: "user-1", Limit: 2, Cursor: timeline.NextCursor})
	require.NoError(t, err)
	require.NotEmpty(t, nextTimeline.Items)

	incidents, err := repository.GetIncidentStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "degraded", incidents.Signals[readmodel.SignalIssuance].Status)
	require.Equal(t, "normal", incidents.Signals[readmodel.SignalRedemption].Status)

	var inboxCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM consumer_inbox WHERE consumer_name='coupon-read-model-projector-v1' AND event_id=$1`, issued.EventID).Scan(&inboxCount))
	require.Equal(t, 1, inboxCount)
}

func TestProjectorAppliesReplayDecisionProductionPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := projectionPostgres(t, ctx)
	projector, err := projection.New(pool, "coupon-read-model-projector-v1")
	require.NoError(t, err)
	repository, err := readmodel.NewPostgresRepository(pool)
	require.NoError(t, err)

	now := time.Date(2026, 7, 12, 6, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		recoveryID  string
		redemption  string
		attemptID   string
		businessKey string
		resultKind  string
		resultRef   string
		failureCode string
		wantStatus  string
	}{
		{
			name: "transitioned", recoveryID: "rcvy_success1", redemption: "redm_success1",
			attemptID: "att_success1", businessKey: "order-success", resultKind: "transitioned",
			resultRef: "payment:success", wantStatus: "completed",
		},
		{
			name: "failed", recoveryID: "rcvy_failure1", redemption: "redm_failure1",
			attemptID: "att_failure1", businessKey: "order-failure", resultKind: "failed",
			failureCode: "coupon.payment_result_verification_failed", wantStatus: "retry_failed",
		},
	}
	for index, test := range tests {
		at := now.Add(time.Duration(index) * time.Minute)
		pending := projectionEvent("EVT.A.19-39", "coupon.recovery.retry_pending", "CouponEventRecovery", test.recoveryID, 1, at, map[string]any{
			"recovery_id": test.recoveryID, "redemption_id": test.redemption,
			"original_operation_type": "confirm", "original_payload_ref": "payload:" + test.recoveryID,
			"attempt_id": test.attemptID, "business_key": test.businessKey, "status": "retry_pending",
			"failure_code": "payment_timeout", "attempt_count": 1, "next_attempt_at": at.Add(time.Minute),
		})
		require.NoError(t, projector.Handle(ctx, pending), test.name)

		// This is the exact payload family produced by redemption.replayDecisionEvent:
		// it deliberately carries result correlation, not the original payload fields.
		decision := projectionEvent("EVT.A.19-41", "coupon.redemption.replay_decided", "CouponRedemption", test.redemption, 2, at.Add(time.Second), map[string]any{
			"recovery_id": test.recoveryID, "attempt_id": test.attemptID,
			"business_key": test.businessKey, "redemption_id": test.redemption,
			"result_kind": test.resultKind, "result_ref": test.resultRef, "failure_code": test.failureCode,
		})
		require.NoError(t, projector.Handle(ctx, decision), test.name)
		require.NoError(t, projector.Handle(ctx, decision), test.name+" replay")
	}

	page, err := repository.ListFailures(ctx, readmodel.FailureQuery{Kind: "recovery", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	byID := make(map[string]readmodel.Failure, len(page.Items))
	for _, item := range page.Items {
		byID[item.FailureID] = item
	}
	for _, test := range tests {
		item := byID["recovery:"+test.recoveryID]
		require.Equal(t, test.wantStatus, item.Status, test.name)
		require.Equal(t, test.attemptID, item.CurrentAttemptID, test.name)
		require.Equal(t, test.resultKind, item.ResultKind, test.name)
		require.Equal(t, test.resultRef, item.ResultRef, test.name)
		require.Equal(t, "confirm", item.OriginalOperation, test.name)
		require.Equal(t, "payload:"+test.recoveryID, item.SourceRef, test.name)
		if test.failureCode != "" {
			require.Equal(t, test.failureCode, item.FailureCode, test.name)
		}
	}
}

func projectionPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
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
	return pool
}

func projectionEvent(documentID, eventType, aggregateType, aggregateID string, version int64, at time.Time, data map[string]any) policy.Envelope {
	return policy.Envelope{
		EventID: uuid.New(), EventDocumentID: documentID, EventType: eventType,
		AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: version,
		OccurredAt: at, CorrelationID: "correlation-1", PayloadSchemaVersion: 1, Data: data,
	}
}
