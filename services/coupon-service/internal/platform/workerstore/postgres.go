package workerstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/jobs"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, oops.In("coupon_worker_store").Code("coupon.worker_store_pool_required").New("postgres pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) ClaimBulkJobs(ctx context.Context, workerID string, now time.Time, limit int, lease time.Duration) ([]jobs.BulkLease, error) {
	if err := validateClaim(workerID, now, limit, lease); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT job.bulk_job_id
			FROM bulk_coupon_issue_jobs AS job
			WHERE job.status IN ('registered','running')
			  AND (job.next_attempt_at IS NULL OR job.next_attempt_at <= $1)
			  AND (job.lease_until IS NULL OR job.lease_until <= $1)
			  AND NOT EXISTS (
				SELECT 1 FROM bulk_coupon_issue_ledger AS ledger
				WHERE ledger.bulk_job_id=job.bulk_job_id
				  AND ledger.event_type='coupon.bulk_issue.page_planned'
				  AND COALESCE((ledger.payload->>'finished')::boolean,false)
			  )
			ORDER BY COALESCE(job.next_attempt_at,job.created_at),job.created_at,job.bulk_job_id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE bulk_coupon_issue_jobs AS job
		SET lease_owner=$3,lease_until=$1+$4::interval,updated_at=$1
		FROM candidates
		WHERE job.bulk_job_id=candidates.bulk_job_id
		RETURNING job.bulk_job_id,job.campaign_id,job.owner_service_id,job.audience_definition_ref,job.audience_snapshot,
			job.evaluation_as_of,job.status,job.planning_complete,job.target_count,job.succeeded_count,job.rejected_count,
			job.failed_count,job.operation_request_ref,job.approval_ref,COALESCE(job.lease_owner,''),
			job.lease_until,job.next_attempt_at,job.attempt_count,job.version,job.created_at,job.updated_at
	`, now, limit, workerID, lease.String())
	if err != nil {
		return nil, dbError("claim_bulk_jobs", err)
	}
	defer rows.Close()
	result := make([]jobs.BulkLease, 0, limit)
	for rows.Next() {
		job, scanErr := scanBulkJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, jobs.BulkLease{Job: job})
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("claim_bulk_rows", err)
	}
	rows.Close()
	for index := range result {
		cursor, page, count, progressErr := s.bulkProgress(ctx, result[index].Job.ID)
		if progressErr != nil {
			return nil, progressErr
		}
		result[index].Cursor = cursor
		result[index].PageNumber = page
		result[index].PlannedTargetCount = count
	}
	return result, nil
}

func (s *PostgresStore) bulkProgress(ctx context.Context, jobID string) (string, int64, int64, error) {
	var cursor string
	var page, count int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(payload->>'next_cursor',''),COALESCE((payload->>'page_number')::bigint,0),
			COALESCE((payload->>'planned_target_count')::bigint,0)
		FROM bulk_coupon_issue_ledger
		WHERE bulk_job_id=$1 AND event_type='coupon.bulk_issue.page_planned'
		ORDER BY COALESCE((payload->>'page_number')::bigint,0) DESC
		LIMIT 1
	`, jobID).Scan(&cursor, &page, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, 0, nil
	}
	if err != nil {
		return "", 0, 0, dbError("read_bulk_progress", err)
	}
	return cursor, page, count, nil
}

func (s *PostgresStore) CommitBulkPage(ctx context.Context, input jobs.BulkPageCommit) (inserted int64, err error) {
	if strings.TrimSpace(input.BulkJobID) == "" || strings.TrimSpace(input.WorkerID) == "" ||
		input.ExpectedVersion < 0 || input.PageNumber < 1 || input.PlannedTargetCount < 0 || input.OccurredAt.IsZero() {
		return 0, inputError("coupon.bulk_page_commit_invalid", "bulk page commit is incomplete")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, dbError("begin_bulk_page", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	job, err := scanBulkJob(tx.QueryRow(ctx, bulkSelect+` WHERE bulk_job_id=$1 FOR UPDATE`, input.BulkJobID))
	if err != nil {
		return 0, err
	}
	if job.Version != input.ExpectedVersion || job.LeaseOwner != input.WorkerID ||
		job.LeaseUntil == nil || !job.LeaseUntil.After(input.OccurredAt) {
		return 0, inputError("coupon.bulk_page_lease_lost", "bulk planner lease or version no longer matches")
	}
	sourceEventID, err := sourceEvent(ctx, tx, input.BulkJobID, []string{"EVT.A.19-16"})
	if err != nil {
		return 0, err
	}
	for _, target := range input.Targets {
		payload, marshalErr := json.Marshal(map[string]any{
			"issueRequestId": target.IssueRequestID, "campaignId": job.CampaignID, "userId": target.UserID,
			"sourceType": "bulk", "sourceRef": input.BulkJobID + ":" + target.UserID,
		})
		if marshalErr != nil {
			return 0, dbError("encode_bulk_command", marshalErr)
		}
		tag, insertErr := tx.Exec(ctx, `
			INSERT INTO coupon_command_requests (
				command_request_id,command_document_id,policy_document_id,source_event_id,
				aggregate_type,aggregate_id,business_key,correlation_id,causation_id,payload
			)
			SELECT $1::uuid,'CMD.A.19-13','POLICY.A.19-11',$2::uuid,'CouponIssueRequest',
				$3::varchar,$4::varchar,$5::varchar,$6::varchar,$7::jsonb
			WHERE NOT EXISTS (
				SELECT 1 FROM coupon_command_requests
				WHERE command_document_id='CMD.A.19-13' AND business_key=$4::varchar
			)
		`, uuid.New(), sourceEventID, target.IssueRequestID, target.BusinessKey, "bulk:"+input.BulkJobID,
			sourceEventID.String(), payload)
		if insertErr != nil {
			return 0, dbError("enqueue_bulk_target", insertErr)
		}
		inserted += tag.RowsAffected()
	}
	total := input.PlannedTargetCount + inserted
	planningComplete := job.PlanningComplete || input.Finished
	job.PlanningComplete = planningComplete
	status := job.Status
	terminalCount := job.SucceededCount + job.RejectedCount + job.FailedCount
	if input.Finished && terminalCount == total {
		status = bulk.StatusCompleted
		if job.RejectedCount > 0 || job.FailedCount > 0 {
			status = bulk.StatusCompletedWithFailures
		}
	}
	var nextAttempt *time.Time
	if !input.Finished {
		nextAttempt = &input.OccurredAt
	}
	tag, err := tx.Exec(ctx, `
		UPDATE bulk_coupon_issue_jobs
		SET status=$2,target_count=$3,planning_complete=$4,lease_owner=NULL,lease_until=NULL,next_attempt_at=$5,
			version=version+1,updated_at=$6
		WHERE bulk_job_id=$1 AND version=$7 AND lease_owner=$8
	`, input.BulkJobID, status, total, planningComplete, nextAttempt, input.OccurredAt, input.ExpectedVersion, input.WorkerID)
	if err != nil {
		return 0, dbError("advance_bulk_page", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, inputError("coupon.bulk_page_lease_lost", "bulk planner lease was lost before page commit")
	}
	progress := map[string]any{
		"current_cursor": input.CurrentCursor, "next_cursor": input.NextCursor,
		"page_number": input.PageNumber, "page_target_count": inserted,
		"planned_target_count": total, "finished": input.Finished,
	}
	payload, err := json.Marshal(progress)
	if err != nil {
		return 0, dbError("encode_bulk_progress", err)
	}
	resultRef := fmt.Sprintf("bulk_page:%s:%d", input.BulkJobID, input.PageNumber)
	if _, err = tx.Exec(ctx, `
		INSERT INTO bulk_coupon_issue_ledger (
			ledger_id,bulk_job_id,event_type,status,target_count,succeeded_count,rejected_count,
			failed_count,result_ref,payload,occurred_at
		) VALUES ($1,$2,'coupon.bulk_issue.page_planned',$3,$4,$5,$6,$7,$8,$9,$10)
	`, uuid.New(), input.BulkJobID, status, total, job.SucceededCount, job.RejectedCount, job.FailedCount, resultRef, payload, input.OccurredAt); err != nil {
		return 0, dbError("append_bulk_progress", err)
	}
	if input.Finished && terminalCount == total {
		eventPayload, marshalErr := json.Marshal(map[string]any{
			"bulk_job_id": input.BulkJobID, "campaign_id": job.CampaignID, "status": status,
			"target_count": total, "succeeded_count": job.SucceededCount,
			"rejected_count": job.RejectedCount, "failed_count": job.FailedCount,
		})
		if marshalErr != nil {
			return 0, dbError("encode_bulk_completion_event", marshalErr)
		}
		eventType, eventDocumentID := "coupon.bulk_issue.completed", "EVT.A.19-17"
		if status == bulk.StatusCompletedWithFailures {
			eventType, eventDocumentID = "coupon.bulk_issue.completed_with_failures", "EVT.A.19-18"
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO domain_outbox (
				event_id,event_type,event_document_id,payload_schema_version,aggregate_type,aggregate_id,
				aggregate_version,correlation_id,causation_id,payload,occurred_at
			) VALUES ($1,$2,$3,1,'BulkCouponIssueJob',$4,$5,$6,$7,$8,$9)
		`, uuid.New(), eventType, eventDocumentID, input.BulkJobID, input.ExpectedVersion+1,
			"bulk:"+input.BulkJobID, input.BulkJobID, eventPayload, input.OccurredAt); err != nil {
			return 0, dbError("append_bulk_completion_event", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, dbError("commit_bulk_page", err)
	}
	committed = true
	return inserted, nil
}

func (s *PostgresStore) FailBulkJob(ctx context.Context, jobID, workerID string, next time.Time, failure jobs.Failure, now time.Time, terminal bool) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError("begin_bulk_failure", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	job, err := scanBulkJob(tx.QueryRow(ctx, bulkSelect+` WHERE bulk_job_id=$1 FOR UPDATE`, jobID))
	if err != nil {
		return err
	}
	if job.LeaseOwner != workerID {
		return inputError("coupon.bulk_page_lease_lost", "bulk planner lease was lost before failure acknowledgement")
	}
	status := job.Status
	var nextAttempt *time.Time
	if terminal {
		status = bulk.StatusFailed
	} else {
		nextAttempt = &next
	}
	if _, err = tx.Exec(ctx, `
		UPDATE bulk_coupon_issue_jobs
		SET status=$2,lease_owner=NULL,lease_until=NULL,next_attempt_at=$3,version=version+1,updated_at=$4
		WHERE bulk_job_id=$1 AND lease_owner=$5
	`, jobID, status, nextAttempt, now, workerID); err != nil {
		return dbError("record_bulk_failure", err)
	}
	payload, marshalErr := json.Marshal(map[string]any{"failure_code": failure.Code, "retryable": failure.Retryable, "terminal": terminal})
	if marshalErr != nil {
		return dbError("encode_bulk_failure", marshalErr)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO bulk_coupon_issue_ledger (
			ledger_id,bulk_job_id,event_type,status,target_count,succeeded_count,rejected_count,
			failed_count,result_ref,payload,occurred_at
		) VALUES ($1,$2,'coupon.bulk_issue.worker_failure',$3,$4,$5,$6,$7,$8,$9,$10)
	`, uuid.New(), jobID, status, job.TargetCount, job.SucceededCount, job.RejectedCount, job.FailedCount,
		"bulk_worker_failure:"+jobID, payload, now); err != nil {
		return dbError("append_bulk_failure", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return dbError("commit_bulk_failure", err)
	}
	committed = true
	return nil
}

func (s *PostgresStore) ClaimIssueRequests(ctx context.Context, workerID string, now time.Time, limit int, lease time.Duration) ([]jobs.IssueLease, error) {
	if err := validateClaim(workerID, now, limit, lease); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT request.issue_request_id
			FROM coupon_issue_requests AS request
			WHERE (
				(request.status IN ('pending','retry_pending') AND (request.next_attempt_at IS NULL OR request.next_attempt_at <= $1))
				OR (
					request.status='processing'
					AND NOT EXISTS (
						SELECT 1 FROM coupon_command_requests AS command
						WHERE command.aggregate_id=request.issue_request_id
						  AND command.command_document_id IN ('CMD.A.19-07','CMD.A.19-23')
					)
				)
			)
			AND (request.lease_until IS NULL OR request.lease_until <= $1)
			ORDER BY COALESCE(request.next_attempt_at,request.created_at),request.issue_request_id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE coupon_issue_requests AS request
		SET lease_owner=$3,lease_until=$1+$4::interval,updated_at=$1
		FROM candidates
		WHERE request.issue_request_id=candidates.issue_request_id
		RETURNING request.issue_request_id,request.campaign_id,request.user_id,request.business_key,
			request.source_type,request.source_ref,request.status,COALESCE(request.user_coupon_id,''),
			COALESCE(request.failure_code,''),request.retry_count,request.next_attempt_at,
			request.issuer_and_funding_snapshot,request.policy_snapshot,COALESCE(request.approval_ref,''),
			COALESCE(request.result_ref,''),COALESCE(request.lease_owner,''),request.lease_until,
			request.version,request.created_at,request.updated_at
	`, now, limit, workerID, lease.String())
	if err != nil {
		return nil, dbError("claim_issue_requests", err)
	}
	defer rows.Close()
	result := make([]jobs.IssueLease, 0, limit)
	for rows.Next() {
		request, scanErr := scanIssueRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, jobs.IssueLease{Request: request})
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("claim_issue_rows", err)
	}
	rows.Close()
	for index := range result {
		lease := &result[index]
		_ = s.pool.QueryRow(ctx, `
			SELECT user_coupon_id,result_ref FROM user_coupons WHERE issue_request_id=$1
		`, lease.Request.ID).Scan(&lease.ExistingUserCouponID, &lease.ExistingResultRef)
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*)+1 FROM coupon_issue_ledger
			WHERE issue_request_id=$1 AND event_type='coupon.issue.worker_failure'
		`, lease.Request.ID).Scan(&lease.WorkerAttempt); err != nil {
			return nil, dbError("count_issue_worker_attempts", err)
		}
	}
	return result, nil
}

func (s *PostgresStore) EnqueueIssueCommand(ctx context.Context, lease jobs.IssueLease, now time.Time) (err error) {
	if strings.TrimSpace(lease.Request.ID) == "" || now.IsZero() {
		return inputError("coupon.issue_enqueue_invalid", "issue request and enqueue time are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError("begin_issue_enqueue", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lease.Request.ID); err != nil {
		return dbError("lock_issue_enqueue", err)
	}
	request, err := scanIssueRequest(tx.QueryRow(ctx, issueSelect+` WHERE issue_request_id=$1 FOR UPDATE`, lease.Request.ID))
	if err != nil {
		return err
	}
	if request.Status != issuerequest.StatusProcessing || request.BusinessKey != lease.Request.BusinessKey {
		return inputError("coupon.issue_enqueue_state_changed", "issue request is no longer the claimed processing request")
	}
	var userCouponID, resultRef string
	lookupErr := tx.QueryRow(ctx, `
		SELECT user_coupon_id,result_ref FROM user_coupons WHERE issue_request_id=$1
	`, request.ID).Scan(&userCouponID, &resultRef)
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		return dbError("read_existing_user_coupon", lookupErr)
	}
	commandID := "CMD.A.19-07"
	policyID := "POLICY.A.19-09"
	aggregateType := "UserCoupon"
	payloadValue := map[string]any{
		"issueRequestId": request.ID, "expectedIssueRequestVersion": request.Version,
	}
	eventDocumentIDs := []string{"EVT.A.19-36", "EVT.A.19-37"}
	sourceAggregateID := request.ID
	if userCouponID != "" {
		commandID = "CMD.A.19-23"
		policyID = "POLICY.A.19-15"
		aggregateType = "CouponIssueRequest"
		payloadValue = map[string]any{
			"issueRequestId": request.ID, "expectedVersion": request.Version, "userCouponId": userCouponID,
			"resultRef": resultRef,
		}
		eventDocumentIDs = []string{"EVT.A.19-09"}
		sourceAggregateID = userCouponID
	}
	payload, marshalErr := json.Marshal(payloadValue)
	if marshalErr != nil {
		return dbError("encode_issue_command", marshalErr)
	}
	sourceEventID, err := sourceEvent(ctx, tx, sourceAggregateID, eventDocumentIDs)
	if err != nil {
		return err
	}
	var existing bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM coupon_command_requests
			WHERE command_document_id=$1 AND aggregate_id=$2
			  AND status IN ('pending','processing')
		)
	`, commandID, request.ID).Scan(&existing); err != nil {
		return dbError("check_issue_command", err)
	}
	if !existing {
		if _, err = tx.Exec(ctx, `
			INSERT INTO coupon_command_requests (
				command_request_id,command_document_id,policy_document_id,source_event_id,
				aggregate_type,aggregate_id,business_key,correlation_id,causation_id,payload
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (policy_document_id,source_event_id,command_document_id,business_key) DO NOTHING
		`, uuid.New(), commandID, policyID, sourceEventID, aggregateType, request.ID, request.BusinessKey,
			"issue:"+request.ID, sourceEventID.String(), payload); err != nil {
			return dbError("enqueue_issue_command", err)
		}
	}
	if _, err = tx.Exec(ctx, `
		UPDATE coupon_issue_requests
		SET lease_owner=$2,lease_until='infinity',updated_at=$3
		WHERE issue_request_id=$1 AND status='processing'
	`, request.ID, "command_queue:"+commandID, now); err != nil {
		return dbError("park_enqueued_issue", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return dbError("commit_issue_enqueue", err)
	}
	committed = true
	return nil
}

func (s *PostgresStore) FailIssueRequest(ctx context.Context, lease jobs.IssueLease, workerID string, next time.Time, failure jobs.Failure, now time.Time, terminal bool) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError("begin_issue_failure", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	request, err := scanIssueRequest(tx.QueryRow(ctx, issueSelect+` WHERE issue_request_id=$1 FOR UPDATE`, lease.Request.ID))
	if err != nil {
		return err
	}
	if request.Status == issuerequest.StatusCompleted || request.Status == issuerequest.StatusRejected || request.Status == issuerequest.StatusFailedFinal {
		// Another command already closed the Aggregate. The stale worker must not
		// clear or replace any later lease state.
	} else {
		leaseActive := lease.Request.LeaseUntil != nil && lease.Request.LeaseUntil.After(now)
		owned := request.LeaseOwner == workerID && request.LeaseUntil != nil && request.LeaseUntil.After(now)
		processingHandoff := request.Status == issuerequest.StatusProcessing && request.LeaseOwner == "" && leaseActive
		if request.Version != lease.Request.Version || request.BusinessKey != lease.Request.BusinessKey || (!owned && !processingHandoff) {
			return inputError("coupon.issue_worker_lease_lost", "issue request version or worker lease changed before failure acknowledgement")
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"worker_attempt": lease.WorkerAttempt, "retryable": failure.Retryable, "terminal": terminal,
		})
		if marshalErr != nil {
			return dbError("encode_issue_failure", marshalErr)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO coupon_issue_ledger (
				ledger_id,issue_request_id,business_key,event_type,status,result_ref,failure_code,payload,occurred_at
			) VALUES ($1,$2,$3,'coupon.issue.worker_failure',$4,NULL,$5,$6,$7)
		`, uuid.New(), request.ID, request.BusinessKey, request.Status, failure.Code, payload, now); err != nil {
			return dbError("append_issue_failure", err)
		}
		if terminal {
			payload, marshalErr = json.Marshal(map[string]any{
				"issueRequestId": request.ID, "expectedIssueRequestVersion": request.Version,
			})
			if marshalErr != nil {
				return dbError("encode_issue_dead_letter", marshalErr)
			}
			sourceEventID, eventErr := sourceEvent(ctx, tx, request.ID, []string{"EVT.A.19-36", "EVT.A.19-37"})
			if eventErr != nil {
				return eventErr
			}
			if _, err = tx.Exec(ctx, `
				INSERT INTO coupon_command_requests (
					command_request_id,command_document_id,policy_document_id,source_event_id,aggregate_type,
					aggregate_id,business_key,correlation_id,causation_id,payload,status,attempt_count,
					next_attempt_at,failure_code
				) VALUES ($1,'CMD.A.19-07','POLICY.A.19-09',$2,'UserCoupon',$3,$4,$5,$6,$7,
					'dead_letter',$8,$9,$10)
				ON CONFLICT (policy_document_id,source_event_id,command_document_id,business_key) DO UPDATE
				SET status='dead_letter',attempt_count=GREATEST(coupon_command_requests.attempt_count,EXCLUDED.attempt_count),
					failure_code=EXCLUDED.failure_code,updated_at=EXCLUDED.next_attempt_at
			`, uuid.New(), sourceEventID, request.ID, request.BusinessKey, "issue:"+request.ID,
				sourceEventID.String(), payload, lease.WorkerAttempt, now, failure.Code); err != nil {
				return dbError("dead_letter_issue", err)
			}
			tag, updateErr := tx.Exec(ctx, `
				UPDATE coupon_issue_requests
				SET lease_owner='dead_letter:CMD.A.19-07',lease_until='infinity',next_attempt_at=NULL,updated_at=$2
				WHERE issue_request_id=$1 AND version=$3 AND (lease_owner=$4 OR lease_owner IS NULL)
			`, request.ID, now, request.Version, workerID)
			if updateErr != nil {
				return dbError("park_dead_letter_issue", updateErr)
			}
			if tag.RowsAffected() != 1 {
				return inputError("coupon.issue_worker_lease_lost", "issue request lease was lost before dead-letter parking")
			}
		} else {
			tag, updateErr := tx.Exec(ctx, `
				UPDATE coupon_issue_requests
				SET lease_owner=NULL,lease_until=NULL,next_attempt_at=$2,updated_at=$3
				WHERE issue_request_id=$1 AND version=$4 AND (lease_owner=$5 OR lease_owner IS NULL)
			`, request.ID, next, now, request.Version, workerID)
			if updateErr != nil {
				return dbError("reschedule_issue", updateErr)
			}
			if tag.RowsAffected() != 1 {
				return inputError("coupon.issue_worker_lease_lost", "issue request lease was lost before rescheduling")
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return dbError("commit_issue_failure", err)
	}
	committed = true
	return nil
}

func (s *PostgresStore) ClaimRecoveries(ctx context.Context, workerID string, now time.Time, limit int, lease time.Duration) ([]jobs.RecoveryLease, error) {
	if err := validateClaim(workerID, now, limit, lease); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT recovery.recovery_id
			FROM coupon_event_recoveries AS recovery
			WHERE recovery.status IN ('retry_pending','retrying')
			  AND (recovery.next_attempt_at IS NULL OR recovery.next_attempt_at <= $1)
			  AND (recovery.lease_until IS NULL OR recovery.lease_until <= $1)
			ORDER BY COALESCE(recovery.next_attempt_at,recovery.created_at),recovery.recovery_id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE coupon_event_recoveries AS recovery
		SET lease_owner=$3,lease_until=$1+$4::interval,updated_at=$1
		FROM candidates
		WHERE recovery.recovery_id=candidates.recovery_id
		RETURNING recovery.recovery_id,recovery.redemption_id,recovery.original_operation_type,recovery.original_payload_ref,
			recovery.original_payload_hash,recovery.business_key,recovery.status,
			COALESCE(recovery.current_attempt_id,''),recovery.attempt_count,recovery.next_attempt_at,
			COALESCE(recovery.result_kind,''),COALESCE(recovery.result_ref,''),
			COALESCE(recovery.failure_code,''),COALESCE(recovery.operation_request_ref,''),
			COALESCE(recovery.approval_ref,''),COALESCE(recovery.lease_owner,''),recovery.lease_until,
			recovery.version,recovery.created_at,recovery.updated_at
	`, now, limit, workerID, lease.String())
	if err != nil {
		return nil, dbError("claim_recoveries", err)
	}
	defer rows.Close()
	result := make([]jobs.RecoveryLease, 0, limit)
	for rows.Next() {
		value, scanErr := scanRecovery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, jobs.RecoveryLease{Recovery: value})
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("claim_recovery_rows", err)
	}
	rows.Close()
	for index := range result {
		lease := &result[index]
		if err := attachRecoveryAttempt(ctx, s.pool, &lease.Recovery); err != nil {
			return nil, err
		}
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*)+1 FROM coupon_recovery_ledger
			WHERE recovery_id=$1 AND event_type='coupon.recovery.worker_failure'
		`, lease.Recovery.ID).Scan(&lease.WorkerAttempt); err != nil {
			return nil, dbError("count_recovery_worker_attempts", err)
		}
	}
	return result, nil
}

func (s *PostgresStore) FailRecovery(ctx context.Context, lease jobs.RecoveryLease, workerID string, next time.Time, failure jobs.Failure, now time.Time, terminal bool) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError("begin_recovery_failure", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	current, err := scanRecovery(tx.QueryRow(ctx, recoverySelect+` WHERE recovery_id=$1 FOR UPDATE`, lease.Recovery.ID))
	if err != nil {
		return err
	}
	if current.CurrentAttemptID != lease.Recovery.CurrentAttemptID || current.BusinessKey != lease.Recovery.BusinessKey {
		return inputError("coupon.recovery_worker_correlation_lost", "recovery attempt correlation changed before failure acknowledgement")
	}
	if current.Version != lease.Recovery.Version || current.LeaseOwner != workerID || current.LeaseUntil == nil || !current.LeaseUntil.After(now) {
		return inputError("coupon.recovery_worker_lease_lost", "recovery version or worker lease changed before failure acknowledgement")
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"worker_attempt": lease.WorkerAttempt, "retryable": failure.Retryable, "terminal": terminal,
	})
	if marshalErr != nil {
		return dbError("encode_recovery_failure", marshalErr)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO coupon_recovery_ledger (
			ledger_id,recovery_id,attempt_id,business_key,event_type,result_ref,failure_code,payload,occurred_at
		) VALUES ($1,$2,NULLIF($3,''),$4,'coupon.recovery.worker_failure',NULL,$5,$6,$7)
	`, uuid.New(), current.ID, current.CurrentAttemptID, current.BusinessKey, failure.Code, payload, now); err != nil {
		return dbError("append_recovery_worker_failure", err)
	}
	if terminal {
		commandPayload, encodeErr := json.Marshal(map[string]any{
			"recoveryId": current.ID, "attemptId": current.CurrentAttemptID, "businessKey": current.BusinessKey,
			"redemptionId": current.RedemptionID, "kind": "failed", "failureCode": failure.Code,
			"retryable": false, "recordedAt": now,
		})
		if encodeErr != nil {
			return dbError("encode_recovery_terminal_result", encodeErr)
		}
		sourceEventID, eventErr := sourceEvent(ctx, tx, current.ID, []string{"EVT.A.19-39"})
		if eventErr != nil {
			return eventErr
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO coupon_command_requests (
				command_request_id,command_document_id,policy_document_id,source_event_id,aggregate_type,aggregate_id,
				business_key,correlation_id,causation_id,payload,status,next_attempt_at,failure_code
			) VALUES ($1,'CMD.A.19-33','POLICY.A.19-22',$2,'CouponEventRecovery',$3,$4,$5,$6,$7,'pending',$8,$9)
			ON CONFLICT (policy_document_id,source_event_id,command_document_id,business_key) DO NOTHING
			`, uuid.New(), sourceEventID, current.ID, "recovery-result:"+uuid.NewSHA1(uuid.NameSpaceOID, []byte(current.ID+"|"+current.CurrentAttemptID+"|"+current.BusinessKey)).String(),
			"recovery:"+current.ID, current.CurrentAttemptID, commandPayload, now, failure.Code); err != nil {
			return dbError("enqueue_recovery_terminal_result", err)
		}
		tag, updateErr := tx.Exec(ctx, `
				UPDATE coupon_event_recoveries
				SET lease_owner='command_queue:CMD.A.19-33',lease_until='infinity',next_attempt_at=NULL,updated_at=$2
				WHERE recovery_id=$1 AND version=$3 AND lease_owner=$4
			`, current.ID, now, current.Version, workerID)
		if updateErr != nil {
			return dbError("park_recovery_terminal_result", updateErr)
		}
		if tag.RowsAffected() != 1 {
			return inputError("coupon.recovery_worker_lease_lost", "recovery lease was lost before terminal result parking")
		}
	} else {
		tag, updateErr := tx.Exec(ctx, `
				UPDATE coupon_event_recoveries
				SET lease_owner=NULL,lease_until=$2,next_attempt_at=$2,updated_at=$3
				WHERE recovery_id=$1 AND version=$4 AND lease_owner=$5
			`, current.ID, next, now, current.Version, workerID)
		if updateErr != nil {
			return dbError("reschedule_recovery", updateErr)
		}
		if tag.RowsAffected() != 1 {
			return inputError("coupon.recovery_worker_lease_lost", "recovery lease was lost before rescheduling")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return dbError("commit_recovery_failure", err)
	}
	committed = true
	return nil
}

func (s *PostgresStore) ClaimExpirations(ctx context.Context, workerID string, now time.Time, limit int, lease time.Duration) ([]jobs.ExpiryLease, error) {
	if err := validateClaim(workerID, now, limit, lease); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT coupon.user_coupon_id
			FROM user_coupons AS coupon
			WHERE coupon.status='granted' AND coupon.expires_at <= $1
			  AND (coupon.expiry_next_attempt_at IS NULL OR coupon.expiry_next_attempt_at <= $1)
			  AND (coupon.expiry_lease_until IS NULL OR coupon.expiry_lease_until <= $1)
			ORDER BY coupon.expires_at,coupon.user_coupon_id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE user_coupons AS coupon
		SET expiry_lease_owner=$3,expiry_lease_until=$1+$4::interval,
			expiry_attempt_count=coupon.expiry_attempt_count+1,updated_at=$1
		FROM candidates
		WHERE coupon.user_coupon_id=candidates.user_coupon_id
		RETURNING coupon.user_coupon_id,coupon.campaign_id,coupon.policy_version,coupon.user_id,
			coupon.issue_request_id,coupon.status,coupon.usable_from,coupon.expires_at,coupon.grant_snapshot,
			coupon.result_ref,coupon.version,coupon.created_at,coupon.updated_at,coupon.expiry_attempt_count
	`, now, limit, workerID, lease.String())
	if err != nil {
		return nil, dbError("claim_expirations", err)
	}
	defer rows.Close()
	result := make([]jobs.ExpiryLease, 0, limit)
	for rows.Next() {
		var lease jobs.ExpiryLease
		if err := rows.Scan(
			&lease.Coupon.ID, &lease.Coupon.CampaignID, &lease.Coupon.PolicyVersion, &lease.Coupon.UserID,
			&lease.Coupon.IssueRequestID, &lease.Coupon.Status, &lease.Coupon.UsableFrom, &lease.Coupon.ExpiresAt,
			&lease.Coupon.GrantSnapshot, &lease.Coupon.ResultRef, &lease.Coupon.Version,
			&lease.Coupon.CreatedAt, &lease.Coupon.UpdatedAt, &lease.Attempt,
		); err != nil {
			return nil, dbError("scan_expiration", err)
		}
		result = append(result, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("claim_expiration_rows", err)
	}
	return result, nil
}

func (s *PostgresStore) CompleteExpiration(ctx context.Context, couponID, workerID string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE user_coupons
		SET expiry_lease_owner=NULL,expiry_lease_until=NULL,expiry_next_attempt_at=NULL,updated_at=$3
		WHERE user_coupon_id=$1 AND expiry_lease_owner=$2 AND status<>'granted'
	`, couponID, workerID, now)
	if err != nil {
		return dbError("complete_expiration", err)
	}
	if tag.RowsAffected() != 1 {
		return inputError("coupon.expiry_lease_lost", "expiration lease was lost before completion")
	}
	return nil
}

func (s *PostgresStore) FailExpiration(ctx context.Context, lease jobs.ExpiryLease, workerID string, next time.Time, failure jobs.Failure, now time.Time, terminal bool) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError("begin_expiry_failure", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	var status usercoupon.Status
	var version int64
	var leaseOwner string
	var leaseUntil *time.Time
	if err = tx.QueryRow(ctx, `
		SELECT status,version,COALESCE(expiry_lease_owner,''),expiry_lease_until
		FROM user_coupons WHERE user_coupon_id=$1 FOR UPDATE
	`, lease.Coupon.ID).Scan(&status, &version, &leaseOwner, &leaseUntil); err != nil {
		return dbError("read_expiry_failure_coupon", err)
	}
	if status != usercoupon.StatusGranted {
		tag, updateErr := tx.Exec(ctx, `
			UPDATE user_coupons SET expiry_lease_owner=NULL,expiry_lease_until=NULL,expiry_next_attempt_at=NULL
			WHERE user_coupon_id=$1 AND expiry_lease_owner=$2
		`, lease.Coupon.ID, workerID)
		if updateErr != nil {
			return dbError("release_resolved_expiration", updateErr)
		}
		if tag.RowsAffected() != 1 {
			return inputError("coupon.expiry_lease_lost", "expiration lease changed after another command resolved the coupon")
		}
	} else {
		if version != lease.Coupon.Version || leaseOwner != workerID || leaseUntil == nil || !leaseUntil.After(now) {
			return inputError("coupon.expiry_lease_lost", "expiration version or worker lease changed before failure acknowledgement")
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"worker_attempt": lease.Attempt, "retryable": failure.Retryable, "terminal": terminal,
		})
		if marshalErr != nil {
			return dbError("encode_expiry_failure", marshalErr)
		}
		resultRef := fmt.Sprintf("expiry_failure:%s:%d", lease.Coupon.ID, lease.Attempt)
		if _, err = tx.Exec(ctx, `
			INSERT INTO user_coupon_ledger (
				ledger_id,user_coupon_id,issue_request_id,event_type,result_ref,payload,occurred_at
			) VALUES ($1,$2,$3,'coupon.user_coupon.expiry_worker_failure',$4,$5,$6)
		`, uuid.New(), lease.Coupon.ID, lease.Coupon.IssueRequestID, resultRef, payload, now); err != nil {
			return dbError("append_expiry_failure", err)
		}
		if terminal {
			commandPayload, encodeErr := json.Marshal(map[string]any{
				"userCouponId": lease.Coupon.ID, "expectedVersion": lease.Coupon.Version, "asOf": lease.Coupon.ExpiresAt,
			})
			if encodeErr != nil {
				return dbError("encode_expiry_dead_letter", encodeErr)
			}
			sourceEventID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("expiry:"+lease.Coupon.ID))
			if _, err = tx.Exec(ctx, `
				INSERT INTO coupon_command_requests (
					command_request_id,command_document_id,policy_document_id,source_event_id,aggregate_type,
					aggregate_id,business_key,correlation_id,causation_id,payload,status,attempt_count,
					next_attempt_at,failure_code
				) VALUES ($1,'CMD.A.19-24','POLICY.A.19-17',$2,'UserCoupon',$3,$4,$5,$6,$7,
					'dead_letter',$8,$9,$10)
				ON CONFLICT (policy_document_id,source_event_id,command_document_id,business_key) DO UPDATE
				SET status='dead_letter',attempt_count=GREATEST(coupon_command_requests.attempt_count,EXCLUDED.attempt_count),
					failure_code=EXCLUDED.failure_code,updated_at=EXCLUDED.next_attempt_at
			`, uuid.New(), sourceEventID, lease.Coupon.ID, "expiry:"+lease.Coupon.ID,
				"expiry:"+lease.Coupon.ID, "CouponExpiryWorker", commandPayload, lease.Attempt, now, failure.Code); err != nil {
				return dbError("dead_letter_expiration", err)
			}
			tag, updateErr := tx.Exec(ctx, `
				UPDATE user_coupons
				SET expiry_lease_owner='dead_letter:CMD.A.19-24',expiry_lease_until='infinity',
					expiry_next_attempt_at=NULL,updated_at=$2
				WHERE user_coupon_id=$1 AND version=$3 AND expiry_lease_owner=$4
			`, lease.Coupon.ID, now, version, workerID)
			if updateErr != nil {
				return dbError("park_dead_letter_expiration", updateErr)
			}
			if tag.RowsAffected() != 1 {
				return inputError("coupon.expiry_lease_lost", "expiration lease was lost before dead-letter parking")
			}
		} else {
			tag, updateErr := tx.Exec(ctx, `
				UPDATE user_coupons
				SET expiry_lease_owner=NULL,expiry_lease_until=NULL,expiry_next_attempt_at=$2,updated_at=$3
				WHERE user_coupon_id=$1 AND version=$4 AND expiry_lease_owner=$5
			`, lease.Coupon.ID, next, now, version, workerID)
			if updateErr != nil {
				return dbError("reschedule_expiration", updateErr)
			}
			if tag.RowsAffected() != 1 {
				return inputError("coupon.expiry_lease_lost", "expiration lease was lost before rescheduling")
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return dbError("commit_expiry_failure", err)
	}
	committed = true
	return nil
}

const bulkSelect = `SELECT bulk_job_id,campaign_id,owner_service_id,audience_definition_ref,audience_snapshot,evaluation_as_of,status,
	planning_complete,target_count,succeeded_count,rejected_count,failed_count,operation_request_ref,approval_ref,
	COALESCE(lease_owner,''),lease_until,next_attempt_at,attempt_count,version,created_at,updated_at
	FROM bulk_coupon_issue_jobs`

const issueSelect = `SELECT issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,
	COALESCE(user_coupon_id,''),COALESCE(failure_code,''),retry_count,next_attempt_at,
	issuer_and_funding_snapshot,policy_snapshot,COALESCE(approval_ref,''),COALESCE(result_ref,''),
	COALESCE(lease_owner,''),lease_until,version,created_at,updated_at
	FROM coupon_issue_requests`

const recoverySelect = `SELECT recovery_id,redemption_id,original_operation_type,original_payload_ref,original_payload_hash,
	business_key,status,COALESCE(current_attempt_id,''),attempt_count,next_attempt_at,
	COALESCE(result_kind,''),COALESCE(result_ref,''),COALESCE(failure_code,''),
	COALESCE(operation_request_ref,''),COALESCE(approval_ref,''),COALESCE(lease_owner,''),
	lease_until,version,created_at,updated_at FROM coupon_event_recoveries`

type rowScanner interface {
	Scan(...any) error
}

func scanBulkJob(row rowScanner) (bulk.Job, error) {
	var job bulk.Job
	var snapshot []byte
	if err := row.Scan(
		&job.ID, &job.CampaignID, &job.OwnerServiceID, &job.AudienceDefinitionRef, &snapshot, &job.EvaluationAsOf,
		&job.Status, &job.PlanningComplete, &job.TargetCount, &job.SucceededCount, &job.RejectedCount, &job.FailedCount,
		&job.OperationRequestRef, &job.ApprovalRef, &job.LeaseOwner, &job.LeaseUntil,
		&job.NextAttemptAt, &job.AttemptCount, &job.Version, &job.CreatedAt, &job.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return bulk.Job{}, inputError("coupon.bulk_job_not_found", "bulk job was not found")
		}
		return bulk.Job{}, dbError("scan_bulk_job", err)
	}
	if err := json.Unmarshal(snapshot, &job.AudienceSnapshot); err != nil {
		return bulk.Job{}, dbError("decode_bulk_snapshot", err)
	}
	return job, nil
}

func scanIssueRequest(row rowScanner) (issuerequest.Request, error) {
	var request issuerequest.Request
	if err := row.Scan(
		&request.ID, &request.CampaignID, &request.UserID, &request.BusinessKey,
		&request.SourceType, &request.SourceRef, &request.Status, &request.UserCouponID,
		&request.FailureCode, &request.RetryCount, &request.NextAttemptAt,
		&request.IssuerAndFundingSnapshot, &request.PolicySnapshot, &request.ApprovalRef,
		&request.ResultRef, &request.LeaseOwner, &request.LeaseUntil, &request.Version,
		&request.CreatedAt, &request.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return issuerequest.Request{}, issuerequest.ErrNotFound
		}
		return issuerequest.Request{}, dbError("scan_issue_request", err)
	}
	return request, nil
}

func scanRecovery(row rowScanner) (recovery.Recovery, error) {
	var value recovery.Recovery
	if err := row.Scan(
		&value.ID, &value.RedemptionID, &value.OriginalOperationType, &value.OriginalPayloadRef, &value.OriginalPayloadHash,
		&value.BusinessKey, &value.Status, &value.CurrentAttemptID, &value.AttemptCount,
		&value.NextAttemptAt, &value.ResultKind, &value.ResultRef, &value.FailureCode,
		&value.OperationRequestRef, &value.ApprovalRef, &value.LeaseOwner, &value.LeaseUntil,
		&value.Version, &value.CreatedAt, &value.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return recovery.Recovery{}, inputError("coupon.recovery_not_found", "coupon recovery was not found")
		}
		return recovery.Recovery{}, dbError("scan_recovery", err)
	}
	return value, nil
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func attachRecoveryAttempt(ctx context.Context, db queryRower, value *recovery.Recovery) error {
	if value.CurrentAttemptID == "" {
		return nil
	}
	var attempt recovery.Attempt
	var retryable *bool
	err := db.QueryRow(ctx, `
		SELECT recovery_id,attempt_id,business_key,status,started_at,finished_at,
			COALESCE(result_kind,''),COALESCE(result_ref,''),COALESCE(failure_code,''),retryable,created_at
		FROM coupon_recovery_attempts
		WHERE recovery_id=$1 AND attempt_id=$2 AND business_key=$3
	`, value.ID, value.CurrentAttemptID, value.BusinessKey).Scan(
		&attempt.RecoveryID, &attempt.ID, &attempt.BusinessKey, &attempt.Status,
		&attempt.StartedAt, &attempt.FinishedAt, &attempt.ResultKind, &attempt.ResultRef,
		&attempt.FailureCode, &retryable, &attempt.CreatedAt,
	)
	if err != nil {
		return dbError("attach_recovery_attempt", err)
	}
	attempt.Retryable = retryable
	value.CurrentAttempt = &attempt
	return nil
}

func sourceEvent(ctx context.Context, tx pgx.Tx, aggregateID string, documentIDs []string) (uuid.UUID, error) {
	var eventID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT event_id FROM domain_outbox
		WHERE aggregate_id=$1 AND event_document_id=ANY($2)
		ORDER BY aggregate_version DESC,event_sequence DESC
		LIMIT 1
	`, aggregateID, documentIDs).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.NewSHA1(uuid.NameSpaceOID, []byte(aggregateID+"|"+strings.Join(documentIDs, ","))), nil
	}
	if err != nil {
		return uuid.Nil, dbError("read_source_event", err)
	}
	return eventID, nil
}

func validateClaim(workerID string, now time.Time, limit int, lease time.Duration) error {
	if strings.TrimSpace(workerID) == "" || now.IsZero() || limit < 1 || lease <= 0 {
		return inputError("coupon.worker_claim_invalid", "worker id, current time, positive limit, and lease are required")
	}
	return nil
}

func inputError(code, message string) error {
	return oops.In("coupon_worker_store").Code(code).New(message)
}

func dbError(operation string, err error) error {
	return oops.In("coupon_worker_store").Code("coupon.worker_store_database_failed").With("operation", operation).Wrap(err)
}

var (
	_ jobs.BulkStore     = (*PostgresStore)(nil)
	_ jobs.IssueStore    = (*PostgresStore)(nil)
	_ jobs.RecoveryStore = (*PostgresStore)(nil)
	_ jobs.ExpiryStore   = (*PostgresStore)(nil)
)
