//go:build integration

package workerstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/jobs"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/Medikong/services/services/coupon-service/internal/platform/workerstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresStoreClaimsBulkAndRecoveryCorrelation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_workerstore"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
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
	snapshot, err := json.Marshal(shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "audience", Type: "definition", ID: "audience-claim"},
		SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO bulk_coupon_issue_jobs (
			bulk_job_id,campaign_id,owner_service_id,audience_definition_ref,audience_snapshot,evaluation_as_of,
			status,planning_complete,operation_request_ref,approval_ref
		) VALUES ('bjob_claimtest','camp_claimtest','operations-service','audience-claim',$1,$2,'registered',false,'task-claim','approval-claim')
	`, snapshot, now)
	require.NoError(t, err)

	store, err := workerstore.NewPostgresStore(pool)
	require.NoError(t, err)
	bulkLeases, err := store.ClaimBulkJobs(ctx, "worker-claim", now, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, bulkLeases, 1)
	require.Equal(t, "bjob_claimtest", bulkLeases[0].Job.ID)
	require.Equal(t, "operations-service", bulkLeases[0].Job.OwnerServiceID)
	require.False(t, bulkLeases[0].Job.PlanningComplete)
	assertBulkTerminalConstraint(t, ctx, pool, snapshot, now)
	assertBulkPageCommandTrace(t, ctx, pool, store, snapshot, now)
	_, err = pool.Exec(ctx, `UPDATE bulk_coupon_issue_jobs SET version=version+1 WHERE bulk_job_id='bjob_claimtest'`)
	require.NoError(t, err)
	bulkRepository := bulk.NewPostgresRepository(pool)
	leasedBulk, err := bulkRepository.Lease(ctx, bulkLeases[0].Job.ID, bulkLeases[0].Job.Version,
		"worker-claim", now.Add(2*time.Minute), now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, "worker-claim", leasedBulk.LeaseOwner)
	require.Greater(t, leasedBulk.Version, bulkLeases[0].Job.Version)

	_, err = pool.Exec(ctx, `
		INSERT INTO coupon_event_recoveries (
			recovery_id,redemption_id,original_operation_type,original_payload_ref,
			original_payload_hash,business_key,status,current_attempt_id,attempt_count,next_attempt_at,version
		) VALUES (
			'rcvy_claimtest','redm_claimtest','confirm','payload-claim',
			'sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA','order-claim','retry_pending',
			'att_claimtest',1,$1,1
		)
	`, now)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO coupon_recovery_attempts (
			recovery_id,attempt_id,business_key,status,created_at
		) VALUES ('rcvy_claimtest','att_claimtest','order-claim','retry_pending',$1)
	`, now)
	require.NoError(t, err)
	recoveryLeases, err := store.ClaimRecoveries(ctx, "worker-recovery", now, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, recoveryLeases, 1)
	require.Equal(t, "redm_claimtest", recoveryLeases[0].Recovery.RedemptionID)
	require.NotNil(t, recoveryLeases[0].Recovery.CurrentAttempt)
	require.Equal(t, "att_claimtest", recoveryLeases[0].Recovery.CurrentAttempt.ID)

	_, err = pool.Exec(ctx, `UPDATE coupon_event_recoveries SET lease_owner='new-worker' WHERE recovery_id='rcvy_claimtest'`)
	require.NoError(t, err)
	err = store.FailRecovery(ctx, recoveryLeases[0], "worker-recovery", now.Add(time.Minute),
		jobs.Failure{Code: "UPSTREAM_TIMEOUT", Retryable: true}, now.Add(time.Second), false)
	require.Error(t, err)
	assertTableCount(t, ctx, pool, "coupon_recovery_ledger", 0)
	_, err = pool.Exec(ctx, `UPDATE coupon_event_recoveries SET lease_owner='worker-recovery' WHERE recovery_id='rcvy_claimtest'`)
	require.NoError(t, err)
	err = store.FailRecovery(ctx, recoveryLeases[0], "worker-recovery", now.Add(time.Minute),
		jobs.Failure{Code: "PAYLOAD_INVALID"}, now.Add(time.Second), true)
	require.NoError(t, err)
	var aggregateID, redemptionID, kind, failureCode, status string
	var commandAttempts int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT aggregate_id,payload->>'redemptionId',payload->>'kind',payload->>'failureCode',status,attempt_count
		FROM coupon_command_requests
		WHERE command_document_id='CMD.A.19-33' AND aggregate_id='rcvy_claimtest'
	`).Scan(&aggregateID, &redemptionID, &kind, &failureCode, &status, &commandAttempts))
	require.Equal(t, "rcvy_claimtest", aggregateID)
	require.Equal(t, "redm_claimtest", redemptionID)
	require.Equal(t, "failed", kind)
	require.Equal(t, "PAYLOAD_INVALID", failureCode)
	require.Equal(t, "pending", status)
	require.Zero(t, commandAttempts)

	assertStaleIssueAndExpiryLeases(t, ctx, pool, store, now)
	assertIssueRetryEnqueuesNewAttempt(t, ctx, pool, store, now)
	assertAppendOnlyLedgers(t, ctx, pool, now)
}

func assertBulkTerminalConstraint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, snapshot []byte, now time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO bulk_coupon_issue_jobs (
			bulk_job_id,campaign_id,owner_service_id,audience_definition_ref,audience_snapshot,evaluation_as_of,
			status,planning_complete,target_count,succeeded_count,rejected_count,failed_count,
			operation_request_ref,approval_ref
		) VALUES ('bjob_constraint1','camp_constraint1','operations-service','audience-constraint',$1,$2,
			'completed',true,10,0,0,0,'task-constraint','approval-constraint')
	`, snapshot, now)
	require.Error(t, err)
}

func assertIssueRetryEnqueuesNewAttempt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *workerstore.PostgresStore, now time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO coupon_issue_requests (
			issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,
			issuer_and_funding_snapshot,policy_snapshot,version
		) VALUES ('ireq_retry0001','camp_retry0001','user-retry','issue:retry','claim','claim:retry',
			'processing','{}'::jsonb,'{}'::jsonb,3)
	`)
	require.NoError(t, err)
	oldSourceEventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO coupon_command_requests (
			command_request_id,command_document_id,policy_document_id,source_event_id,
			aggregate_type,aggregate_id,business_key,correlation_id,payload,status,result_ref
		) VALUES ($1,'CMD.A.19-07','POLICY.A.19-09',$2,'UserCoupon','ireq_retry0001',
			'issue:retry','issue:ireq_retry0001','{}'::jsonb,'completed','user_coupon:old')
	`, uuid.New(), oldSourceEventID)
	require.NoError(t, err)
	retryEventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO domain_outbox (
			event_id,event_type,event_document_id,aggregate_type,aggregate_id,aggregate_version,
			correlation_id,payload,occurred_at
		) VALUES ($1,'coupon.issue.retry_pending','EVT.A.19-37','CouponIssueRequest',
			'ireq_retry0001',3,'issue:ireq_retry0001','{}'::jsonb,$2)
	`, retryEventID, now)
	require.NoError(t, err)

	err = store.EnqueueIssueCommand(ctx, jobs.IssueLease{Request: issuerequest.Request{
		ID: "ireq_retry0001", BusinessKey: "issue:retry", Status: issuerequest.StatusProcessing, Version: 3,
	}}, now)
	require.NoError(t, err)
	var active int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM coupon_command_requests
		WHERE command_document_id='CMD.A.19-07' AND aggregate_id='ireq_retry0001' AND status='pending'
	`).Scan(&active))
	require.Equal(t, 1, active)
}

func assertBulkPageCommandTrace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *workerstore.PostgresStore, snapshot []byte, now time.Time) {
	t.Helper()

	_, err := pool.Exec(ctx, `
		INSERT INTO bulk_coupon_issue_jobs (
			bulk_job_id,campaign_id,owner_service_id,audience_definition_ref,audience_snapshot,evaluation_as_of,
			status,planning_complete,operation_request_ref,approval_ref,lease_owner,lease_until,version
		) VALUES (
			'bjob_policytest','camp_policytest','operations-service','audience-policy',$1,$2,
			'running',false,'task-policy','approval-policy','worker-policy',$3,1
		)
	`, snapshot, now, now.Add(time.Minute))
	require.NoError(t, err)
	sourceEventID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO domain_outbox (
			event_id,event_type,event_document_id,aggregate_type,aggregate_id,aggregate_version,
			correlation_id,payload,occurred_at
		) VALUES ($1,'coupon.bulk_issue.registered','EVT.A.19-16','BulkCouponIssueJob',
			'bjob_policytest',0,'bulk:bjob_policytest','{}'::jsonb,$2)
	`, sourceEventID, now)
	require.NoError(t, err)

	inserted, err := store.CommitBulkPage(ctx, jobs.BulkPageCommit{
		BulkJobID: "bjob_policytest", ExpectedVersion: 1, WorkerID: "worker-policy",
		PageNumber: 1, Targets: []jobs.BulkTarget{{
			UserID: "user-policy", BusinessKey: "bjob_policytest:user-policy", IssueRequestID: "ireq_policytest",
		}}, Finished: true, OccurredAt: now.Add(time.Second),
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, inserted)

	var policyDocumentID, causationID string
	var recordedSourceEventID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT policy_document_id,source_event_id,causation_id
		FROM coupon_command_requests
		WHERE command_document_id='CMD.A.19-13' AND business_key='bjob_policytest:user-policy'
	`).Scan(&policyDocumentID, &recordedSourceEventID, &causationID))
	require.Equal(t, "POLICY.A.19-11", policyDocumentID)
	require.Equal(t, sourceEventID, recordedSourceEventID)
	require.Equal(t, sourceEventID.String(), causationID)
}

func assertStaleIssueAndExpiryLeases(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *workerstore.PostgresStore, now time.Time) {
	t.Helper()
	issueLeaseUntil := now.Add(time.Minute)
	_, err := pool.Exec(ctx, `
		INSERT INTO coupon_issue_requests (
			issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,
			issuer_and_funding_snapshot,policy_snapshot,lease_owner,lease_until,version
		) VALUES ('ireq_stale001','camp_stale001','user-stale','claim:user-stale','claim','api:claim',
			'pending','{}'::jsonb,'{}'::jsonb,'new-worker',$1,1)
	`, issueLeaseUntil)
	require.NoError(t, err)
	err = store.FailIssueRequest(ctx, jobs.IssueLease{Request: issuerequest.Request{
		ID: "ireq_stale001", BusinessKey: "claim:user-stale", Status: issuerequest.StatusPending,
		LeaseOwner: "old-worker", LeaseUntil: &issueLeaseUntil, Version: 1,
	}}, "old-worker", now.Add(time.Minute), jobs.Failure{Code: "TIMEOUT", Retryable: true}, now, false)
	require.Error(t, err)
	assertTableCount(t, ctx, pool, "coupon_issue_ledger", 0)
	_, err = pool.Exec(ctx, `UPDATE coupon_issue_requests SET lease_owner='old-worker' WHERE issue_request_id='ireq_stale001'`)
	require.NoError(t, err)
	err = store.FailIssueRequest(ctx, jobs.IssueLease{Request: issuerequest.Request{
		ID: "ireq_stale001", BusinessKey: "claim:user-stale", Status: issuerequest.StatusPending,
		LeaseOwner: "old-worker", LeaseUntil: &issueLeaseUntil, Version: 1,
	}}, "old-worker", now.Add(time.Minute), jobs.Failure{Code: "TIMEOUT", Retryable: true}, now, false)
	require.NoError(t, err)
	assertTableCount(t, ctx, pool, "coupon_issue_ledger", 1)

	_, err = pool.Exec(ctx, `
		INSERT INTO user_coupons (
			user_coupon_id,campaign_id,policy_version,user_id,issue_request_id,status,usable_from,
			expires_at,grant_snapshot,result_ref,expiry_lease_owner,expiry_lease_until,version
		) VALUES ('ucpn_stale001','camp_stale001',1,'user-stale','ireq_stale001','granted',$1,$2,
			'{}'::jsonb,'grant:stale','new-worker',$3,1)
	`, now.Add(-2*time.Hour), now.Add(-time.Hour), issueLeaseUntil)
	require.NoError(t, err)
	err = store.FailExpiration(ctx, jobs.ExpiryLease{Coupon: usercoupon.Coupon{
		ID: "ucpn_stale001", IssueRequestID: "ireq_stale001", Version: 1,
	}}, "old-worker", now.Add(time.Minute), jobs.Failure{Code: "TIMEOUT", Retryable: true}, now, false)
	require.Error(t, err)
	assertTableCount(t, ctx, pool, "user_coupon_ledger", 0)
	_, err = pool.Exec(ctx, `UPDATE user_coupons SET expiry_lease_owner='old-worker' WHERE user_coupon_id='ucpn_stale001'`)
	require.NoError(t, err)
	err = store.FailExpiration(ctx, jobs.ExpiryLease{Coupon: usercoupon.Coupon{
		ID: "ucpn_stale001", IssueRequestID: "ireq_stale001", Version: 1,
	}}, "old-worker", now.Add(time.Minute), jobs.Failure{Code: "TIMEOUT", Retryable: true}, now, false)
	require.NoError(t, err)
	assertTableCount(t, ctx, pool, "user_coupon_ledger", 1)
}

func assertAppendOnlyLedgers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	t.Helper()
	seeds := []struct {
		table  string
		insert string
	}{
		{"coupon_quantity_ledger", `INSERT INTO coupon_quantity_ledger (ledger_id,campaign_id,issue_request_id,transition,quantity,after_state,result_ref,occurred_at) VALUES ($1,'camp_append','ireq_append','reserve',1,'reserved','append:quantity',$2)`},
		{"coupon_issue_ledger", `INSERT INTO coupon_issue_ledger (ledger_id,issue_request_id,business_key,event_type,status,result_ref,payload,occurred_at) VALUES ($1,'ireq_append','append:issue','test.append_only','pending','append:issue','{}'::jsonb,$2)`},
		{"user_coupon_ledger", `INSERT INTO user_coupon_ledger (ledger_id,user_coupon_id,issue_request_id,event_type,result_ref,payload,occurred_at) VALUES ($1,'ucpn_append','ireq_append','test.append_only','append:user_coupon','{}'::jsonb,$2)`},
		{"coupon_redemption_ledger", `INSERT INTO coupon_redemption_ledger (ledger_id,redemption_id,order_id,user_coupon_id,event_type,amount_snapshot,result_ref,payload,occurred_at) VALUES ($1,'redm_append','order-append','ucpn_append','test.append_only','{}'::jsonb,'append:redemption','{}'::jsonb,$2)`},
		{"coupon_operation_ledger", `INSERT INTO coupon_operation_ledger (ledger_id,control_id,scope,operation_request_ref,approval_ref,event_type,payload,occurred_at) VALUES ($1,'ctrl_append','{}'::jsonb,'task-append','approval-append','test.append_only','{}'::jsonb,$2)`},
		{"coupon_recovery_ledger", `INSERT INTO coupon_recovery_ledger (ledger_id,recovery_id,business_key,event_type,payload,occurred_at) VALUES ($1,'rcvy_append','append:recovery','test.append_only','{}'::jsonb,$2)`},
		{"bulk_coupon_issue_ledger", `INSERT INTO bulk_coupon_issue_ledger (ledger_id,bulk_job_id,event_type,status,target_count,succeeded_count,rejected_count,failed_count,result_ref,payload,occurred_at) VALUES ($1,'bjob_append','test.append_only','registered',0,0,0,0,'append:bulk','{}'::jsonb,$2)`},
	}
	for _, seed := range seeds {
		ledgerID := uuid.New()
		_, err := pool.Exec(ctx, seed.insert, ledgerID, now)
		require.NoError(t, err, seed.table)
		_, err = pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET occurred_at=occurred_at WHERE ledger_id=$1`, seed.table), ledgerID)
		require.ErrorContains(t, err, "coupon ledgers are append-only", seed.table)
	}
}

func assertTableCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, expected int) {
	t.Helper()
	var actual int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&actual))
	require.Equal(t, expected, actual, table)
}
