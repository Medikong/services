package issuerequest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

const aggregateType = "CouponIssueRequest"

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) (*PostgresRepository, error) {
	if db == nil {
		return nil, oops.In("issue_request_repository").Code("issue_request.database_required").New("postgres pool is required")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) Create(ctx context.Context, request Request, admission Admission, command Command) (Mutation, error) {
	if err := request.Validate(); err != nil {
		return Mutation{}, err
	}
	if request.Status != StatusAccepted || admission.PerUserLimit <= 0 {
		return Mutation{}, ErrInvalidTransition
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, request.ID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, request.CampaignID+"\n"+request.UserID); err != nil {
			return Mutation{}, dbError("lock_per_user_limit", err)
		}
		var admitted int64
		if err := tx.QueryRow(ctx, `
			SELECT count(DISTINCT issue_request_id) FROM (
				SELECT issue_request_id FROM coupon_issue_requests
				WHERE campaign_id=$1 AND user_id=$2 AND status NOT IN ('rejected','failed_final')
				UNION
				SELECT issue_request_id FROM user_coupons WHERE campaign_id=$1 AND user_id=$2
			) admitted_requests
		`, request.CampaignID, request.UserID).Scan(&admitted); err != nil {
			return Mutation{}, dbError("count_per_user_limit", err)
		}
		if admitted >= admission.PerUserLimit {
			return Mutation{}, ErrPerUserLimitExceeded
		}
		resultRef := strings.Join([]string{"issue_request", request.ID, "accepted"}, ":")
		request.ResultRef = resultRef
		tag, err := tx.Exec(ctx, `
			INSERT INTO coupon_issue_requests (
				issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,
				user_coupon_id,failure_code,retry_count,next_attempt_at,issuer_and_funding_snapshot,
				policy_snapshot,approval_ref,result_ref,lease_owner,lease_until,version
			) VALUES ($1,$2,$3,$4,$5,$6,'accepted',NULL,NULL,$7,$8,$9,$10,NULLIF($11,''),$12,NULL,NULL,$13)
			ON CONFLICT (campaign_id,user_id,business_key) DO NOTHING`,
			request.ID, request.CampaignID, request.UserID, request.BusinessKey, request.SourceType, request.SourceRef,
			request.RetryCount, request.NextAttemptAt, request.IssuerAndFundingSnapshot, request.PolicySnapshot,
			request.ApprovalRef, request.ResultRef, request.Version)
		if err != nil {
			return Mutation{}, dbError("create_issue_request", err)
		}
		if tag.RowsAffected() == 0 {
			existing, err := loadByBusinessKey(ctx, tx, request.CampaignID, request.UserID, request.BusinessKey, true)
			if err != nil {
				return Mutation{}, err
			}
			if existing.ID != request.ID || existing.SourceType != request.SourceType || existing.SourceRef != request.SourceRef {
				return Mutation{}, ErrIdempotencyConflict
			}
			mutation := replayMutation(existing)
			if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		payload := requestPayload(request)
		mutation := Mutation{Request: request, ResultRef: resultRef, ResponseSnapshot: payload}
		if err := insertLedger(ctx, tx, request, "coupon.issue.accepted", resultRef, payload, command.OccurredAt); err != nil {
			return Mutation{}, err
		}
		if err := insertOutbox(ctx, tx, command, "coupon.issue.accepted", "EVT.A.19-07", request.ID, request.Version, payload); err != nil {
			return Mutation{}, err
		}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

func (r *PostgresRepository) Get(ctx context.Context, requestID string) (Request, error) {
	return loadByID(ctx, r.db, requestID, false)
}

func (r *PostgresRepository) FindDue(ctx context.Context, asOf time.Time, limit int) ([]Request, error) {
	if limit <= 0 {
		return nil, oops.In("issue_request_repository").Code("issue_request.limit_invalid").New("due request limit must be positive")
	}
	rows, err := r.db.Query(ctx, `SELECT issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,COALESCE(user_coupon_id,''),COALESCE(failure_code,''),retry_count,next_attempt_at,issuer_and_funding_snapshot,policy_snapshot,COALESCE(approval_ref,''),COALESCE(result_ref,''),COALESCE(lease_owner,''),lease_until,version,created_at,updated_at FROM coupon_issue_requests WHERE status IN ('pending','retry_pending') AND (next_attempt_at IS NULL OR next_attempt_at<=$1) AND (lease_until IS NULL OR lease_until<=$1) ORDER BY next_attempt_at NULLS FIRST,issue_request_id LIMIT $2`, asOf, limit)
	if err != nil {
		return nil, dbError("find_due", err)
	}
	defer rows.Close()
	var requests []Request
	for rows.Next() {
		var request Request
		if err := scanRequest(rows, &request); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("find_due", err)
	}
	return requests, nil
}

func (r *PostgresRepository) MarkPending(ctx context.Context, requestID string, expectedVersion int64, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) { return current.MarkPending() }, "coupon.issue.pending", "EVT.A.19-36", true)
}

func (r *PostgresRepository) MarkProcessing(ctx context.Context, requestID string, expectedVersion int64, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) { return current.MarkProcessing() }, "coupon.issue.processing", "", false)
}

func (r *PostgresRepository) RecordFailure(ctx context.Context, requestID string, expectedVersion int64, failureCode string, retryable bool, nextAttemptAt *time.Time, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) {
		return current.RecordFailure(failureCode, retryable, nextAttemptAt)
	}, "coupon.issue.failed_retryable", "EVT.A.19-10", true)
}

func (r *PostgresRepository) Retry(ctx context.Context, requestID string, expectedVersion int64, nextAttemptAt time.Time, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) { return current.Retry(nextAttemptAt) }, "coupon.issue.retry_pending", "EVT.A.19-37", true)
}

func (r *PostgresRepository) Reject(ctx context.Context, requestID string, expectedVersion int64, failureCode string, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) { return current.Reject(failureCode) }, "coupon.issue.rejected", "EVT.A.19-08", true)
}

func (r *PostgresRepository) Complete(ctx context.Context, requestID string, expectedVersion int64, userCouponID string, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) { return current.Complete(userCouponID) }, "coupon.issue.completed", "EVT.A.19-29", true)
}

func (r *PostgresRepository) FinalizeFailure(ctx context.Context, requestID string, expectedVersion int64, failureCode string, command Command) (Mutation, error) {
	return r.transition(ctx, requestID, expectedVersion, command, func(current Request) (Request, error) { return current.FinalizeFailure(failureCode) }, "coupon.issue.failed_final", "EVT.A.19-11", true)
}

func (r *PostgresRepository) transition(ctx context.Context, requestID string, expectedVersion int64, command Command, mutate func(Request) (Request, error), eventType, documentID string, publish bool) (Mutation, error) {
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, requestID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		current, err := loadByID(ctx, tx, requestID, true)
		if err != nil {
			return Mutation{}, err
		}
		if current.Version != expectedVersion {
			return Mutation{}, ErrVersionConflict
		}
		updated, err := mutate(current)
		if err != nil {
			return Mutation{}, err
		}
		if updated.Version == current.Version {
			mutation := replayMutation(current)
			if err := finishIdempotency(ctx, tx, command, terminalIdempotencyStatus(current.Status), mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		resultRef := strings.Join([]string{"issue_request", requestID, string(updated.Status)}, ":")
		updated.ResultRef = resultRef
		updated.UpdatedAt = command.OccurredAt
		tag, err := tx.Exec(ctx, `UPDATE coupon_issue_requests SET status=$1,user_coupon_id=NULLIF($2,''),failure_code=NULLIF($3,''),retry_count=$4,next_attempt_at=$5,result_ref=$6,lease_owner=NULL,lease_until=NULL,version=$7,updated_at=$8 WHERE issue_request_id=$9 AND version=$10`, updated.Status, updated.UserCouponID, updated.FailureCode, updated.RetryCount, updated.NextAttemptAt, resultRef, updated.Version, command.OccurredAt, requestID, expectedVersion)
		if err != nil {
			return Mutation{}, dbError("transition_issue_request", err)
		}
		if tag.RowsAffected() != 1 {
			return Mutation{}, ErrVersionConflict
		}
		payload := requestPayload(updated)
		mutation := Mutation{Request: updated, ResultRef: resultRef, ResponseSnapshot: payload}
		if err := insertLedger(ctx, tx, updated, eventType, resultRef, payload, command.OccurredAt); err != nil {
			return Mutation{}, err
		}
		if publish {
			if err := insertOutbox(ctx, tx, command, eventType, documentID, requestID, updated.Version, payload); err != nil {
				return Mutation{}, err
			}
		}
		if err := finishIdempotency(ctx, tx, command, terminalIdempotencyStatus(updated.Status), mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

type rowScanner interface {
	Scan(...any) error
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadByID(ctx context.Context, db queryer, requestID string, lock bool) (Request, error) {
	return loadRequest(ctx, db, `issue_request_id=$1`, []any{requestID}, lock)
}

func loadByBusinessKey(ctx context.Context, db queryer, campaignID, userID, businessKey string, lock bool) (Request, error) {
	return loadRequest(ctx, db, `campaign_id=$1 AND user_id=$2 AND business_key=$3`, []any{campaignID, userID, businessKey}, lock)
}

func loadRequest(ctx context.Context, db queryer, condition string, args []any, lock bool) (Request, error) {
	query := `SELECT issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,COALESCE(user_coupon_id,''),COALESCE(failure_code,''),retry_count,next_attempt_at,issuer_and_funding_snapshot,policy_snapshot,COALESCE(approval_ref,''),COALESCE(result_ref,''),COALESCE(lease_owner,''),lease_until,version,created_at,updated_at FROM coupon_issue_requests WHERE ` + condition
	if lock {
		query += ` FOR UPDATE`
	}
	var request Request
	if err := scanRequest(db.QueryRow(ctx, query, args...), &request); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Request{}, ErrNotFound
		}
		return Request{}, err
	}
	return request, nil
}

func scanRequest(row rowScanner, request *Request) error {
	var nextAttemptAt, leaseUntil sql.NullTime
	if err := row.Scan(&request.ID, &request.CampaignID, &request.UserID, &request.BusinessKey, &request.SourceType, &request.SourceRef, &request.Status, &request.UserCouponID, &request.FailureCode, &request.RetryCount, &nextAttemptAt, &request.IssuerAndFundingSnapshot, &request.PolicySnapshot, &request.ApprovalRef, &request.ResultRef, &request.LeaseOwner, &leaseUntil, &request.Version, &request.CreatedAt, &request.UpdatedAt); err != nil {
		return dbError("scan_issue_request", err)
	}
	if nextAttemptAt.Valid {
		request.NextAttemptAt = &nextAttemptAt.Time
	}
	if leaseUntil.Valid {
		request.LeaseUntil = &leaseUntil.Time
	}
	return nil
}

func insertLedger(ctx context.Context, tx pgx.Tx, request Request, eventType, resultRef string, payload []byte, occurredAt time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO coupon_issue_ledger (ledger_id,issue_request_id,business_key,event_type,status,result_ref,failure_code,payload,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9)`, uuid.New(), request.ID, request.BusinessKey, eventType, request.Status, resultRef, request.FailureCode, payload, occurredAt)
	if err != nil {
		return dbError("insert_issue_ledger", err)
	}
	return nil
}

type idempotencyResult struct {
	mutation Mutation
	replayed bool
}

func acquireIdempotency(ctx context.Context, tx pgx.Tx, command Command, ownerID string) (idempotencyResult, error) {
	if command.OperationType == "" || command.BusinessKey == "" || command.RequestHash == "" || command.CorrelationID == "" || command.OccurredAt.IsZero() ||
		!command.LeaseUntil.After(command.OccurredAt) || !command.ExpiresAt.After(command.LeaseUntil) {
		return idempotencyResult{}, oops.In("issue_request_repository").Code("issue_request.command_invalid").New("command idempotency, correlation, lease, and expiry fields are required")
	}
	digest := sha256.Sum256([]byte(command.RequestHash))
	tag, err := tx.Exec(ctx, `INSERT INTO coupon_idempotency_records (operation_type,business_key,owner_type,owner_id,request_hash,status,locked_until,expires_at) VALUES ($1,$2,$3,$4,$5,'processing',$6,$7) ON CONFLICT DO NOTHING`, command.OperationType, command.BusinessKey, aggregateType, ownerID, digest[:], command.LeaseUntil, command.ExpiresAt)
	if err != nil {
		return idempotencyResult{}, dbError("claim_idempotency", err)
	}
	if tag.RowsAffected() == 1 {
		return idempotencyResult{}, nil
	}
	var storedHash, snapshot []byte
	var status string
	var resultRef sql.NullString
	var lockedUntil sql.NullTime
	err = tx.QueryRow(ctx, `SELECT request_hash,status,result_ref,response_snapshot,locked_until FROM coupon_idempotency_records WHERE operation_type=$1 AND business_key=$2 FOR UPDATE`, command.OperationType, command.BusinessKey).Scan(&storedHash, &status, &resultRef, &snapshot, &lockedUntil)
	if err != nil {
		return idempotencyResult{}, dbError("read_idempotency", err)
	}
	if !bytes.Equal(storedHash, digest[:]) {
		return idempotencyResult{}, ErrIdempotencyConflict
	}
	if status == "processing" {
		if lockedUntil.Valid && lockedUntil.Time.After(command.OccurredAt) {
			return idempotencyResult{}, ErrCommandInProgress
		}
		tag, err = tx.Exec(ctx, `UPDATE coupon_idempotency_records SET owner_type=$3,owner_id=$4,locked_until=$5,expires_at=$6,updated_at=$7 WHERE operation_type=$1 AND business_key=$2 AND status='processing'`, command.OperationType, command.BusinessKey, aggregateType, ownerID, command.LeaseUntil, command.ExpiresAt, command.OccurredAt)
		if err != nil {
			return idempotencyResult{}, dbError("resume_idempotency", err)
		}
		if tag.RowsAffected() != 1 {
			return idempotencyResult{}, ErrCommandInProgress
		}
		return idempotencyResult{}, nil
	}
	var request Request
	if err := json.Unmarshal(snapshot, &request); err != nil {
		return idempotencyResult{}, oops.In("issue_request_repository").Code("issue_request.snapshot_decode_failed").Wrap(err)
	}
	return idempotencyResult{mutation: Mutation{Request: request, ResultRef: resultRef.String, ResponseSnapshot: snapshot, Replayed: true}, replayed: true}, nil
}

func finishIdempotency(ctx context.Context, tx pgx.Tx, command Command, status string, mutation Mutation) error {
	digest := sha256.Sum256([]byte(command.RequestHash))
	tag, err := tx.Exec(ctx, `UPDATE coupon_idempotency_records SET status=$1,result_ref=$2,response_snapshot=$3,locked_until=NULL,completed_at=$4,updated_at=$4 WHERE operation_type=$5 AND business_key=$6 AND request_hash=$7 AND status='processing'`, status, mutation.ResultRef, mutation.ResponseSnapshot, command.OccurredAt, command.OperationType, command.BusinessKey, digest[:])
	if err != nil {
		return dbError("finish_idempotency", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrCommandInProgress
	}
	return nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, command Command, eventType, documentID, aggregateID string, version int64, payload []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO domain_outbox (event_id,event_type,event_document_id,payload_schema_version,aggregate_type,aggregate_id,aggregate_version,correlation_id,causation_id,trace_id,payload,occurred_at) VALUES ($1,$2,$3,1,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11)`, uuid.New(), eventType, documentID, aggregateType, aggregateID, version, command.CorrelationID, command.CausationID, command.TraceID, payload, command.OccurredAt)
	if err != nil {
		return dbError("insert_outbox", err)
	}
	return nil
}

func terminalIdempotencyStatus(status Status) string {
	if status == StatusRejected || status == StatusFailedFinal {
		return "failed_final"
	}
	return "completed"
}

func requestPayload(request Request) []byte {
	payload, _ := json.Marshal(request)
	return payload
}

func replayMutation(request Request) Mutation {
	return Mutation{Request: request, ResultRef: request.ResultRef, ResponseSnapshot: requestPayload(request), Replayed: true}
}

func dbError(operation string, err error) error {
	return oops.In("issue_request_repository").Code("issue_request.database_failed").With("operation", operation).Wrap(err)
}

func inTx[T any](ctx context.Context, db *pgxpool.Pool, run func(pgx.Tx) (T, error)) (result T, err error) {
	tx, beginErr := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if beginErr != nil {
		return result, dbError("begin_transaction", beginErr)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	result, err = run(tx)
	if err != nil {
		return result, err
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return result, dbError("commit_transaction", commitErr)
	}
	committed = true
	return result, nil
}
