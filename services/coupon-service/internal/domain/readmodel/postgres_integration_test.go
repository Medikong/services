//go:build integration

package readmodel_test

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestBulkJobSummaryUsesCorrelatedRedemptionAndProgressLedgers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_bulk_summary"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
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
	_, err = pool.Exec(ctx, `INSERT INTO coupon_issue_requests (
		issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,user_coupon_id,
		issuer_and_funding_snapshot,policy_snapshot,result_ref,version
	) VALUES ('ireq_summary001','camp_summary001','user-summary','bulk:summary','bulk','bjob_summary001:user-summary',
		'completed','ucpn_summary001','{}','{}','issue_request:ireq_summary001:completed',3)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_coupons (
		user_coupon_id,campaign_id,policy_version,user_id,issue_request_id,status,usable_from,expires_at,
		grant_snapshot,result_ref,version
	) VALUES ('ucpn_summary001','camp_summary001',1,'user-summary','ireq_summary001','granted',$1,$2,
		'{}','user_coupon:ucpn_summary001:granted',1)`, now, now.Add(24*time.Hour))
	require.NoError(t, err)
	for index, eventType := range []string{"coupon.redemption.reserved", "coupon.redemption.confirmed", "coupon.redemption.reclaimed"} {
		_, err = pool.Exec(ctx, `INSERT INTO coupon_redemption_ledger (
			ledger_id,redemption_id,order_id,user_coupon_id,event_type,amount_snapshot,result_ref,payload,occurred_at
		) VALUES ($1,'redm_summary001','order:summary','ucpn_summary001',$2,'{}','result:summary','{}',$3)`,
			uuid.New(), eventType, now.Add(time.Duration(index)*time.Second))
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO bulk_coupon_issue_ledger (
		ledger_id,bulk_job_id,event_type,status,target_count,succeeded_count,rejected_count,failed_count,
		result_ref,payload,occurred_at
	) VALUES ($1,'bjob_summary001','coupon.bulk_issue.page_planned','running',1,0,0,0,
		'bulk_page:bjob_summary001:1','{"next_cursor":"cursor:next-1"}',$2)`, uuid.New(), now.Add(4*time.Second))
	require.NoError(t, err)

	repository, err := readmodel.NewPostgresRepository(pool)
	require.NoError(t, err)
	summary, err := repository.BulkJobSummary(ctx, "bjob_summary001")
	require.NoError(t, err)
	require.EqualValues(t, 1, summary.Counts.Reserved)
	require.EqualValues(t, 1, summary.Counts.Confirmed)
	require.EqualValues(t, 0, summary.Counts.Released)
	require.EqualValues(t, 1, summary.Counts.Reclaimed)
	require.Equal(t, "cursor:next-1", summary.NextCursorRef)
	require.Equal(t, now.Add(4*time.Second), summary.AsOf)
}
