package bulk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

var _ Repository = (*PostgresRepository)(nil)

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, job Job, domainEvent reliability.Event, command reliability.Command) (result Job, err error) {
	if err = r.ready(); err != nil {
		return Job{}, err
	}
	if err = job.Validate(); err != nil {
		return Job{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, dbError("begin_create", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_bulk_repository", err)
		}
	}()
	if result, done, claimErr := replayJob(ctx, tx, command, job.ID); claimErr != nil || done {
		return commitReplay(ctx, tx, result, claimErr, &committed)
	}
	snapshot, err := json.Marshal(job.AudienceSnapshot)
	if err != nil {
		return Job{}, oops.In("coupon_bulk_repository").Code("coupon.bulk_snapshot_encode_failed").Wrap(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO bulk_coupon_issue_jobs (
			bulk_job_id,campaign_id,owner_service_id,audience_definition_ref,audience_snapshot,evaluation_as_of,status,planning_complete,
			target_count,succeeded_count,rejected_count,failed_count,operation_request_ref,approval_ref,
			lease_owner,lease_until,next_attempt_at,attempt_count,version,created_at,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,''),$16,$17,$18,$19,$20,$21)
	`, job.ID, job.CampaignID, job.OwnerServiceID, job.AudienceDefinitionRef, snapshot, job.EvaluationAsOf, job.Status,
		job.PlanningComplete, job.TargetCount, job.SucceededCount, job.RejectedCount, job.FailedCount, job.OperationRequestRef, job.ApprovalRef,
		job.LeaseOwner, job.LeaseUntil, job.NextAttemptAt, job.AttemptCount, job.Version, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return Job{}, dbError("insert_job", err)
	}
	domainEvent.CorrelationID = command.CorrelationID
	domainEvent.CausationID = command.CausationID
	domainEvent.TraceID = command.TraceID
	if err = appendLedger(ctx, tx, job, domainEvent.Type, job.ID, domainEvent.OccurredAt); err != nil {
		return Job{}, err
	}
	if err = reliability.AppendOutbox(ctx, tx, domainEvent); err != nil {
		return Job{}, err
	}
	if err = reliability.Complete(ctx, tx, command, job.ID, job); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Job{}, dbError("commit_create", err)
	}
	committed = true
	return job, nil
}

func (r *PostgresRepository) Find(ctx context.Context, id string) (Job, error) {
	if err := r.ready(); err != nil {
		return Job{}, err
	}
	return scanJob(r.pool.QueryRow(ctx, bulkSelect+` WHERE bulk_job_id=$1`, id))
}

// HasResultRef prevents a terminal target result from being counted again
// when its command committed but the queue acknowledgement was lost.
func (r *PostgresRepository) HasResultRef(ctx context.Context, id, resultRef string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(resultRef) == "" {
		return false, oops.In("coupon_bulk_repository").Code("coupon.bulk_result_correlation_invalid").New("bulk job and result reference are required")
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM bulk_coupon_issue_ledger
		WHERE bulk_job_id=$1 AND result_ref=$2
		  AND event_type IN ('coupon.bulk_issue.result_aggregated','coupon.bulk_issue.completed','coupon.bulk_issue.completed_with_failures')
	)`, id, resultRef).Scan(&exists); err != nil {
		return false, dbError("find_result_ref", err)
	}
	return exists, nil
}

func (r *PostgresRepository) FindDue(ctx context.Context, now time.Time, limit int) ([]Job, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, oops.In("coupon_bulk_repository").Code("coupon.limit_invalid").New("bulk job query limit must be positive")
	}
	rows, err := r.pool.Query(ctx, bulkSelect+`
		WHERE status IN ('registered','running')
		  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		  AND (lease_until IS NULL OR lease_until <= $1)
		ORDER BY COALESCE(next_attempt_at,created_at),created_at,bulk_job_id LIMIT $2`, now, limit)
	if err != nil {
		return nil, dbError("find_due", err)
	}
	defer rows.Close()
	jobs := make([]Job, 0, limit)
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, dbError("find_due", err)
	}
	return jobs, nil
}

func (r *PostgresRepository) Lease(ctx context.Context, id string, expectedVersion int64, owner string, until, now time.Time) (result Job, err error) {
	if err = r.ready(); err != nil {
		return Job{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, dbError("begin_lease", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_bulk_repository", err)
		}
	}()
	result, err = scanJob(tx.QueryRow(ctx, bulkSelect+` WHERE bulk_job_id=$1 FOR UPDATE`, id))
	if err != nil {
		return Job{}, err
	}
	leaseVersion := expectedVersion
	if result.LeaseOwner == owner && result.LeaseUntil != nil && result.LeaseUntil.After(now) {
		// The worker store claims a job before this Aggregate mutation. Result
		// aggregation may legitimately advance the version in between; the
		// still-current owner may adopt that version and refresh its lease.
		leaseVersion = result.Version
	}
	if err = result.Lease(leaseVersion, owner, until, now); err != nil {
		return Job{}, err
	}
	if err = updateJob(ctx, tx, result); err != nil {
		return Job{}, err
	}
	if err = appendLedger(ctx, tx, result, "coupon.bulk_issue.leased", result.ID, now); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Job{}, dbError("commit_lease", err)
	}
	committed = true
	return result, nil
}

func (r *PostgresRepository) AggregateResult(ctx context.Context, id string, expectedVersion int64, delta ResultDelta, command reliability.Command) (result Job, err error) {
	if err = r.ready(); err != nil {
		return Job{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, dbError("begin_aggregate", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_bulk_repository", err)
		}
	}()
	if result, done, claimErr := replayJob(ctx, tx, command, id); claimErr != nil || done {
		return commitReplay(ctx, tx, result, claimErr, &committed)
	}
	result, err = scanJob(tx.QueryRow(ctx, bulkSelect+` WHERE bulk_job_id=$1 FOR UPDATE`, id))
	if err != nil {
		return Job{}, err
	}
	alreadyAggregated, err := hasAggregatedResult(ctx, tx, id, delta.ResultRef)
	if err != nil {
		return Job{}, err
	}
	if alreadyAggregated {
		if err = reliability.Complete(ctx, tx, command, delta.ResultRef, result); err != nil {
			return Job{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Job{}, dbError("commit_aggregate_replay", err)
		}
		committed = true
		return result, nil
	}
	domainEvent, err := result.AggregateResult(expectedVersion, delta)
	if err != nil {
		return Job{}, err
	}
	if err = updateJob(ctx, tx, result); err != nil {
		return Job{}, err
	}
	ledgerType := "coupon.bulk_issue.result_aggregated"
	if domainEvent.Type != "" {
		ledgerType = domainEvent.Type
	}
	if err = appendLedger(ctx, tx, result, ledgerType, delta.ResultRef, result.UpdatedAt); err != nil {
		return Job{}, err
	}
	if domainEvent.Type != "" {
		domainEvent.CorrelationID = command.CorrelationID
		domainEvent.CausationID = command.CausationID
		domainEvent.TraceID = command.TraceID
		if err = reliability.AppendOutbox(ctx, tx, domainEvent); err != nil {
			return Job{}, err
		}
	}
	if err = reliability.Complete(ctx, tx, command, delta.ResultRef, result); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Job{}, dbError("commit_aggregate", err)
	}
	committed = true
	return result, nil
}

func hasAggregatedResult(ctx context.Context, tx pgx.Tx, id, resultRef string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM bulk_coupon_issue_ledger
		WHERE bulk_job_id=$1 AND result_ref=$2
		  AND event_type IN ('coupon.bulk_issue.result_aggregated','coupon.bulk_issue.completed','coupon.bulk_issue.completed_with_failures')
	)`, id, resultRef).Scan(&exists)
	if err != nil {
		return false, dbError("find_aggregated_result", err)
	}
	return exists, nil
}

const bulkSelect = `SELECT bulk_job_id,campaign_id,owner_service_id,audience_definition_ref,audience_snapshot,evaluation_as_of,status,
	planning_complete,target_count,succeeded_count,rejected_count,failed_count,operation_request_ref,approval_ref,
	COALESCE(lease_owner,''),lease_until,next_attempt_at,attempt_count,version,created_at,updated_at FROM bulk_coupon_issue_jobs`

type rowScanner interface {
	Scan(...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var snapshot []byte
	err := row.Scan(&job.ID, &job.CampaignID, &job.OwnerServiceID, &job.AudienceDefinitionRef, &snapshot, &job.EvaluationAsOf, &job.Status,
		&job.PlanningComplete, &job.TargetCount, &job.SucceededCount, &job.RejectedCount, &job.FailedCount, &job.OperationRequestRef, &job.ApprovalRef,
		&job.LeaseOwner, &job.LeaseUntil, &job.NextAttemptAt, &job.AttemptCount, &job.Version, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, oops.In("coupon_bulk_repository").Code("coupon.bulk_job_not_found").New("bulk coupon issue job was not found")
	}
	if err != nil {
		return Job{}, dbError("scan_job", err)
	}
	if err = json.Unmarshal(snapshot, &job.AudienceSnapshot); err != nil {
		return Job{}, oops.In("coupon_bulk_repository").Code("coupon.bulk_snapshot_decode_failed").Wrap(err)
	}
	return job, nil
}

func updateJob(ctx context.Context, tx pgx.Tx, job Job) error {
	result, err := tx.Exec(ctx, `UPDATE bulk_coupon_issue_jobs SET status=$2,planning_complete=$3,target_count=$4,succeeded_count=$5,
		rejected_count=$6,failed_count=$7,lease_owner=NULLIF($8,''),lease_until=$9,next_attempt_at=$10,
		attempt_count=$11,version=$12,updated_at=$13 WHERE bulk_job_id=$1 AND version=$14`,
		job.ID, job.Status, job.PlanningComplete, job.TargetCount, job.SucceededCount, job.RejectedCount, job.FailedCount,
		job.LeaseOwner, job.LeaseUntil, job.NextAttemptAt, job.AttemptCount, job.Version, job.UpdatedAt, job.Version-1)
	if err != nil {
		return dbError("update_job", err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_bulk_repository").Code("coupon.version_conflict").New("bulk coupon issue job changed concurrently")
	}
	return nil
}

func appendLedger(ctx context.Context, tx pgx.Tx, job Job, eventType, resultRef string, occurredAt time.Time) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return oops.In("coupon_bulk_repository").Code("coupon.bulk_ledger_encode_failed").Wrap(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO bulk_coupon_issue_ledger (ledger_id,bulk_job_id,event_type,status,target_count,
		succeeded_count,rejected_count,failed_count,result_ref,payload,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		uuid.New(), job.ID, eventType, job.Status, job.TargetCount, job.SucceededCount, job.RejectedCount, job.FailedCount, resultRef, payload, occurredAt)
	if err != nil {
		return dbError("append_ledger", err)
	}
	return nil
}

func replayJob(ctx context.Context, tx pgx.Tx, command reliability.Command, id string) (Job, bool, error) {
	replay, err := reliability.Claim(ctx, tx, command, "BulkCouponIssueJob", id)
	if err != nil {
		return Job{}, false, err
	}
	if !replay.Existing || replay.Resume {
		return Job{}, false, nil
	}
	if replay.Status != "completed" {
		return Job{}, false, oops.In("coupon_bulk_repository").Code("coupon.command_in_progress").New("bulk coupon issue command is already processing")
	}
	if len(replay.ResponseSnapshot) > 0 {
		var job Job
		if err = json.Unmarshal(replay.ResponseSnapshot, &job); err != nil {
			return Job{}, false, oops.In("coupon_bulk_repository").Code("coupon.idempotency_snapshot_decode_failed").Wrap(err)
		}
		return job, true, nil
	}
	job, err := scanJob(tx.QueryRow(ctx, bulkSelect+` WHERE bulk_job_id=$1`, id))
	return job, true, err
}

func commitReplay(ctx context.Context, tx pgx.Tx, result Job, cause error, committed *bool) (Job, error) {
	if cause != nil {
		return Job{}, cause
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, dbError("commit_replay", err)
	}
	*committed = true
	return result, nil
}

func (r *PostgresRepository) ready() error {
	if r == nil || r.pool == nil {
		return oops.In("coupon_bulk_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	return nil
}

func dbError(operation string, err error) error {
	return oops.In("coupon_bulk_repository").Code("coupon.database_failed").With("operation", operation).Wrap(err)
}
