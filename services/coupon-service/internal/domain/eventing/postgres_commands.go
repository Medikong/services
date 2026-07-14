package eventing

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresCommandQueue struct {
	pool *pgxpool.Pool
}

func NewPostgresCommandQueue(pool *pgxpool.Pool) *PostgresCommandQueue {
	return &PostgresCommandQueue{pool: pool}
}

func (q *PostgresCommandQueue) SubmitCommand(ctx context.Context, input CommandSubmission) (uuid.UUID, error) {
	if q == nil || q.pool == nil {
		return uuid.Nil, oops.In("coupon_command_queue").Code("coupon.pool_required").New("postgres pool is required")
	}
	if input.ID == uuid.Nil || !strings.HasPrefix(input.CommandDocumentID, "CMD.A.19-") ||
		strings.TrimSpace(input.AggregateType) == "" || strings.TrimSpace(input.AggregateID) == "" ||
		strings.TrimSpace(input.BusinessKey) == "" || strings.TrimSpace(input.CorrelationID) == "" ||
		!json.Valid(input.Payload) || input.NotBefore.IsZero() {
		return uuid.Nil, oops.In("coupon_command_queue").Code("coupon.command_submission_invalid").New("command submission identity, target, correlation, payload, and schedule are required")
	}
	tag, err := q.pool.Exec(ctx, `
		INSERT INTO coupon_command_requests (
			command_request_id,command_document_id,aggregate_type,aggregate_id,business_key,
			correlation_id,causation_id,trace_id,payload,next_attempt_at
		) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10)
		ON CONFLICT (command_request_id) DO NOTHING
	`, input.ID, input.CommandDocumentID, input.AggregateType, input.AggregateID, input.BusinessKey,
		input.CorrelationID, input.CausationID, input.TraceID, input.Payload, input.NotBefore.UTC())
	if err != nil {
		return uuid.Nil, oops.In("coupon_command_queue").Code("coupon.command_submission_failed").Wrap(err)
	}
	if tag.RowsAffected() == 1 {
		return input.ID, nil
	}
	var matches bool
	err = q.pool.QueryRow(ctx, `
		SELECT command_document_id=$2 AND aggregate_type=$3 AND aggregate_id=$4 AND business_key=$5 AND
			correlation_id=$6 AND COALESCE(causation_id,'')=$7 AND COALESCE(trace_id,'')=$8 AND payload=$9::jsonb
		FROM coupon_command_requests WHERE command_request_id=$1
	`, input.ID, input.CommandDocumentID, input.AggregateType, input.AggregateID, input.BusinessKey,
		input.CorrelationID, input.CausationID, input.TraceID, input.Payload).Scan(&matches)
	if err != nil {
		return uuid.Nil, oops.In("coupon_command_queue").Code("coupon.command_submission_replay_read_failed").Wrap(err)
	}
	if !matches {
		return uuid.Nil, oops.In("coupon_command_queue").Code("coupon.command_submission_conflict").New("command idempotency key was reused with different input")
	}
	return input.ID, nil
}

func (q *PostgresCommandQueue) ClaimCommands(ctx context.Context, workerID string, batch int, lease time.Duration) ([]CommandRequest, error) {
	if q == nil || q.pool == nil {
		return nil, oops.In("coupon_command_queue").Code("coupon.pool_required").New("postgres pool is required")
	}
	rows, err := q.pool.Query(ctx, `
		WITH candidates AS (
			SELECT command_request_id
			FROM coupon_command_requests
			WHERE (
				(status='pending' AND next_attempt_at <= now()) OR
				(status='processing' AND lease_until < now())
			)
			ORDER BY created_at, command_request_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE coupon_command_requests AS item
		SET status='processing', lease_owner=$2, lease_until=now()+$3::interval,
			attempt_count=item.attempt_count+1, updated_at=now()
		FROM candidates
		WHERE item.command_request_id=candidates.command_request_id
		RETURNING item.command_request_id, item.command_document_id,
			COALESCE(item.policy_document_id,''), item.source_event_id,
			item.aggregate_type, item.aggregate_id, item.business_key,
			item.correlation_id, COALESCE(item.causation_id,''), COALESCE(item.trace_id,''), item.payload,
			item.attempt_count, item.lease_owner, item.lease_until
	`, batch, workerID, lease.String())
	if err != nil {
		return nil, oops.In("coupon_command_queue").Code("coupon.command_claim_failed").Wrap(err)
	}
	defer rows.Close()
	items := make([]CommandRequest, 0, batch)
	for rows.Next() {
		var item CommandRequest
		if err := rows.Scan(
			&item.ID, &item.CommandDocumentID, &item.PolicyDocumentID, &item.SourceEventID,
			&item.AggregateType, &item.AggregateID, &item.BusinessKey,
			&item.CorrelationID, &item.CausationID, &item.TraceID, &item.Payload,
			&item.AttemptCount, &item.LeaseOwner, &item.LeaseUntil,
		); err != nil {
			return nil, oops.In("coupon_command_queue").Code("coupon.command_scan_failed").Wrap(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("coupon_command_queue").Code("coupon.command_rows_failed").Wrap(err)
	}
	return items, nil
}

func (q *PostgresCommandQueue) CompleteCommand(ctx context.Context, id uuid.UUID, workerID, resultRef string) error {
	result, err := q.pool.Exec(ctx, `
		UPDATE coupon_command_requests
		SET status='completed', result_ref=$3, lease_owner=NULL, lease_until=NULL, updated_at=now()
		WHERE command_request_id=$1 AND status='processing' AND lease_owner=$2
	`, id, workerID, resultRef)
	if err != nil {
		return oops.In("coupon_command_queue").Code("coupon.command_complete_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_command_queue").Code("coupon.command_lease_lost").New("command request lease was lost before completion")
	}
	return nil
}

func (q *PostgresCommandQueue) FailCommand(ctx context.Context, id uuid.UUID, workerID string, next time.Time, code string, terminal bool) error {
	status := "pending"
	if terminal {
		status = "dead_letter"
	}
	result, err := q.pool.Exec(ctx, `
		UPDATE coupon_command_requests
		SET status=$3, next_attempt_at=$4, failure_code=$5,
			lease_owner=NULL, lease_until=NULL, updated_at=now()
		WHERE command_request_id=$1 AND status='processing' AND lease_owner=$2
	`, id, workerID, status, next, code)
	if err != nil {
		return oops.In("coupon_command_queue").Code("coupon.command_failure_record_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_command_queue").Code("coupon.command_lease_lost").New("command request lease was lost before failure acknowledgement")
	}
	return nil
}

var _ CommandQueue = (*PostgresCommandQueue)(nil)
var _ CommandSubmitter = (*PostgresCommandQueue)(nil)
