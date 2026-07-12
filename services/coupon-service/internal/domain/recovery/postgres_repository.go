package recovery

import (
	"context"
	"encoding/json"
	"errors"
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

func (r *PostgresRepository) RecordFailure(ctx context.Context, recovery Recovery, domainEvent reliability.Event, command reliability.Command) (result Recovery, err error) {
	if err = r.ready(); err != nil {
		return Recovery{}, err
	}
	if err = recovery.Validate(); err != nil {
		return Recovery{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Recovery{}, dbError("begin_record_failure", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_recovery_repository", err)
		}
	}()
	if result, done, replayErr := replayRecovery(ctx, tx, command, recovery.ID); replayErr != nil || done {
		return commitReplay(ctx, tx, result, replayErr, &committed)
	}
	if err = insertRecovery(ctx, tx, recovery); err != nil {
		return Recovery{}, err
	}
	if err = persistEvent(ctx, tx, recovery, domainEvent, command); err != nil {
		return Recovery{}, err
	}
	if err = reliability.Complete(ctx, tx, command, recovery.ID, recovery); err != nil {
		return Recovery{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Recovery{}, dbError("commit_record_failure", err)
	}
	committed = true
	return recovery, nil
}

func (r *PostgresRepository) Find(ctx context.Context, id string) (Recovery, error) {
	if err := r.ready(); err != nil {
		return Recovery{}, err
	}
	return findRecovery(ctx, r.pool, id, false)
}

func (r *PostgresRepository) FindDue(ctx context.Context, now time.Time, limit int) ([]Recovery, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, oops.In("coupon_recovery_repository").Code("coupon.limit_invalid").New("coupon recovery query limit must be positive")
	}
	rows, err := r.pool.Query(ctx, recoverySelect+`
		WHERE status='retry_pending' AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		  AND (lease_until IS NULL OR lease_until <= $1)
		ORDER BY COALESCE(next_attempt_at,created_at),recovery_id LIMIT $2`, now, limit)
	if err != nil {
		return nil, dbError("find_due", err)
	}
	defer rows.Close()
	items := make([]Recovery, 0, limit)
	for rows.Next() {
		item, scanErr := scanRecovery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, dbError("find_due", err)
	}
	rows.Close()
	for i := range items {
		if err = attachAttempt(ctx, r.pool, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *PostgresRepository) RequestRetry(ctx context.Context, id string, input RetryRequest, command reliability.Command) (result Recovery, err error) {
	return r.mutate(ctx, id, command, func(tx pgx.Tx, current *Recovery) (reliability.Event, error) {
		attempt, domainEvent, mutateErr := current.RequestRetry(input)
		if mutateErr != nil {
			return reliability.Event{}, mutateErr
		}
		if insertErr := insertAttempt(ctx, tx, attempt); insertErr != nil {
			return reliability.Event{}, insertErr
		}
		return domainEvent, nil
	})
}

func (r *PostgresRepository) Lease(ctx context.Context, id string, expectedVersion int64, attemptID, businessKey, owner string, until, now time.Time) (result Recovery, err error) {
	if err = r.ready(); err != nil {
		return Recovery{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Recovery{}, dbError("begin_lease", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_recovery_repository", err)
		}
	}()
	result, err = findRecovery(ctx, tx, id, true)
	if err != nil {
		return Recovery{}, err
	}
	if err = result.Lease(expectedVersion, attemptID, businessKey, owner, until, now); err != nil {
		return Recovery{}, err
	}
	if err = updateRecovery(ctx, tx, result); err != nil {
		return Recovery{}, err
	}
	if err = updateAttempt(ctx, tx, *result.CurrentAttempt); err != nil {
		return Recovery{}, err
	}
	if err = appendLedger(ctx, tx, result, "coupon.recovery.retrying", now); err != nil {
		return Recovery{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Recovery{}, dbError("commit_lease", err)
	}
	committed = true
	return result, nil
}

func (r *PostgresRepository) RecordResult(ctx context.Context, id string, input ReplayResult, command reliability.Command) (Recovery, error) {
	return r.mutate(ctx, id, command, func(tx pgx.Tx, current *Recovery) (reliability.Event, error) {
		domainEvent, mutateErr := current.RecordResult(input)
		if mutateErr != nil {
			return reliability.Event{}, mutateErr
		}
		if updateErr := updateAttempt(ctx, tx, *current.CurrentAttempt); updateErr != nil {
			return reliability.Event{}, updateErr
		}
		return domainEvent, nil
	})
}

func (r *PostgresRepository) Finalize(ctx context.Context, id string, input Finalization, command reliability.Command) (Recovery, error) {
	return r.mutate(ctx, id, command, func(_ pgx.Tx, current *Recovery) (reliability.Event, error) {
		return current.Finalize(input)
	})
}

func (r *PostgresRepository) mutate(ctx context.Context, id string, command reliability.Command, change func(pgx.Tx, *Recovery) (reliability.Event, error)) (result Recovery, err error) {
	if err = r.ready(); err != nil {
		return Recovery{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Recovery{}, dbError("begin_mutation", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_recovery_repository", err)
		}
	}()
	if result, done, replayErr := replayRecovery(ctx, tx, command, id); replayErr != nil || done {
		return commitReplay(ctx, tx, result, replayErr, &committed)
	}
	result, err = findRecovery(ctx, tx, id, true)
	if err != nil {
		return Recovery{}, err
	}
	domainEvent, err := change(tx, &result)
	if err != nil {
		return Recovery{}, err
	}
	if err = updateRecovery(ctx, tx, result); err != nil {
		return Recovery{}, err
	}
	if err = persistEvent(ctx, tx, result, domainEvent, command); err != nil {
		return Recovery{}, err
	}
	if err = reliability.Complete(ctx, tx, command, result.ID, result); err != nil {
		return Recovery{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Recovery{}, dbError("commit_mutation", err)
	}
	committed = true
	return result, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const recoverySelect = `SELECT recovery_id,redemption_id,original_operation_type,original_payload_ref,original_payload_hash,business_key,
	status,COALESCE(current_attempt_id,''),attempt_count,next_attempt_at,COALESCE(result_kind,''),COALESCE(result_ref,''),
	COALESCE(failure_code,''),COALESCE(operation_request_ref,''),COALESCE(approval_ref,''),COALESCE(lease_owner,''),
	lease_until,version,created_at,updated_at FROM coupon_event_recoveries`

type rowScanner interface {
	Scan(...any) error
}

func scanRecovery(row rowScanner) (Recovery, error) {
	var value Recovery
	err := row.Scan(&value.ID, &value.RedemptionID, &value.OriginalOperationType, &value.OriginalPayloadRef, &value.OriginalPayloadHash,
		&value.BusinessKey, &value.Status, &value.CurrentAttemptID, &value.AttemptCount, &value.NextAttemptAt,
		&value.ResultKind, &value.ResultRef, &value.FailureCode, &value.OperationRequestRef, &value.ApprovalRef,
		&value.LeaseOwner, &value.LeaseUntil, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recovery{}, oops.In("coupon_recovery_repository").Code("coupon.recovery_not_found").New("coupon event recovery was not found")
	}
	if err != nil {
		return Recovery{}, dbError("scan_recovery", err)
	}
	return value, nil
}

func findRecovery(ctx context.Context, db queryer, id string, lock bool) (Recovery, error) {
	query := recoverySelect + ` WHERE recovery_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	value, err := scanRecovery(db.QueryRow(ctx, query, id))
	if err != nil {
		return Recovery{}, err
	}
	if err = attachAttempt(ctx, db, &value); err != nil {
		return Recovery{}, err
	}
	return value, nil
}

func attachAttempt(ctx context.Context, db queryer, value *Recovery) error {
	if value.CurrentAttemptID == "" {
		return nil
	}
	var attempt Attempt
	var retryable *bool
	err := db.QueryRow(ctx, `SELECT recovery_id,attempt_id,business_key,status,started_at,finished_at,
		COALESCE(result_kind,''),COALESCE(result_ref,''),COALESCE(failure_code,''),retryable,created_at
		FROM coupon_recovery_attempts WHERE recovery_id=$1 AND attempt_id=$2 AND business_key=$3`,
		value.ID, value.CurrentAttemptID, value.BusinessKey).Scan(&attempt.RecoveryID, &attempt.ID, &attempt.BusinessKey,
		&attempt.Status, &attempt.StartedAt, &attempt.FinishedAt, &attempt.ResultKind, &attempt.ResultRef,
		&attempt.FailureCode, &retryable, &attempt.CreatedAt)
	if err != nil {
		return dbError("read_attempt", err)
	}
	attempt.Retryable = retryable
	value.CurrentAttempt = &attempt
	return nil
}

func insertRecovery(ctx context.Context, tx pgx.Tx, value Recovery) error {
	_, err := tx.Exec(ctx, `INSERT INTO coupon_event_recoveries (
		recovery_id,redemption_id,original_operation_type,original_payload_ref,original_payload_hash,business_key,status,
		current_attempt_id,attempt_count,next_attempt_at,result_kind,result_ref,failure_code,
		operation_request_ref,approval_ref,lease_owner,lease_until,version,created_at,updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),
		NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),$17,$18,$19,$20)`,
		value.ID, value.RedemptionID, value.OriginalOperationType, value.OriginalPayloadRef, value.OriginalPayloadHash, value.BusinessKey,
		value.Status, value.CurrentAttemptID, value.AttemptCount, value.NextAttemptAt, value.ResultKind, value.ResultRef,
		value.FailureCode, value.OperationRequestRef, value.ApprovalRef, value.LeaseOwner, value.LeaseUntil,
		value.Version, value.CreatedAt, value.UpdatedAt)
	if err != nil {
		return dbError("insert_recovery", err)
	}
	return nil
}

func updateRecovery(ctx context.Context, tx pgx.Tx, value Recovery) error {
	result, err := tx.Exec(ctx, `UPDATE coupon_event_recoveries SET status=$2,current_attempt_id=NULLIF($3,''),
		attempt_count=$4,next_attempt_at=$5,result_kind=NULLIF($6,''),result_ref=NULLIF($7,''),failure_code=NULLIF($8,''),
		operation_request_ref=NULLIF($9,''),approval_ref=NULLIF($10,''),lease_owner=NULLIF($11,''),lease_until=$12,
		version=$13,updated_at=$14 WHERE recovery_id=$1 AND version=$15`, value.ID, value.Status, value.CurrentAttemptID,
		value.AttemptCount, value.NextAttemptAt, value.ResultKind, value.ResultRef, value.FailureCode,
		value.OperationRequestRef, value.ApprovalRef, value.LeaseOwner, value.LeaseUntil, value.Version, value.UpdatedAt, value.Version-1)
	if err != nil {
		return dbError("update_recovery", err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_recovery_repository").Code("coupon.version_conflict").New("coupon event recovery changed concurrently")
	}
	return nil
}

func insertAttempt(ctx context.Context, tx pgx.Tx, value Attempt) error {
	_, err := tx.Exec(ctx, `INSERT INTO coupon_recovery_attempts (recovery_id,attempt_id,business_key,status,
		started_at,finished_at,result_kind,result_ref,failure_code,retryable,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),$10,$11)`,
		value.RecoveryID, value.ID, value.BusinessKey, value.Status, value.StartedAt, value.FinishedAt,
		value.ResultKind, value.ResultRef, value.FailureCode, value.Retryable, value.CreatedAt)
	if err != nil {
		return dbError("insert_attempt", err)
	}
	return nil
}

func updateAttempt(ctx context.Context, tx pgx.Tx, value Attempt) error {
	result, err := tx.Exec(ctx, `UPDATE coupon_recovery_attempts SET status=$4,started_at=$5,finished_at=$6,
		result_kind=NULLIF($7,''),result_ref=NULLIF($8,''),failure_code=NULLIF($9,''),retryable=$10
		WHERE recovery_id=$1 AND attempt_id=$2 AND business_key=$3`, value.RecoveryID, value.ID, value.BusinessKey,
		value.Status, value.StartedAt, value.FinishedAt, value.ResultKind, value.ResultRef, value.FailureCode, value.Retryable)
	if err != nil {
		return dbError("update_attempt", err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_recovery_repository").Code("coupon.recovery_attempt_missing").New("current coupon recovery attempt was not found")
	}
	return nil
}

func persistEvent(ctx context.Context, tx pgx.Tx, value Recovery, domainEvent reliability.Event, command reliability.Command) error {
	domainEvent.CorrelationID = command.CorrelationID
	domainEvent.CausationID = command.CausationID
	domainEvent.TraceID = command.TraceID
	if err := appendLedger(ctx, tx, value, domainEvent.Type, domainEvent.OccurredAt); err != nil {
		return err
	}
	return reliability.AppendOutbox(ctx, tx, domainEvent)
}

func appendLedger(ctx context.Context, tx pgx.Tx, value Recovery, eventType string, occurredAt time.Time) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return oops.In("coupon_recovery_repository").Code("coupon.recovery_ledger_encode_failed").Wrap(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO coupon_recovery_ledger (ledger_id,recovery_id,attempt_id,business_key,
		event_type,result_ref,failure_code,payload,occurred_at) VALUES ($1,$2,NULLIF($3,''),$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9)`,
		uuid.New(), value.ID, value.CurrentAttemptID, value.BusinessKey, eventType, value.ResultRef, value.FailureCode, payload, occurredAt)
	if err != nil {
		return dbError("append_ledger", err)
	}
	return nil
}

func replayRecovery(ctx context.Context, tx pgx.Tx, command reliability.Command, id string) (Recovery, bool, error) {
	replay, err := reliability.Claim(ctx, tx, command, "CouponEventRecovery", id)
	if err != nil {
		return Recovery{}, false, err
	}
	if !replay.Existing || replay.Resume {
		return Recovery{}, false, nil
	}
	if replay.Status != "completed" {
		return Recovery{}, false, oops.In("coupon_recovery_repository").Code("coupon.command_in_progress").New("coupon recovery command is already processing")
	}
	if len(replay.ResponseSnapshot) > 0 {
		var value Recovery
		if err = json.Unmarshal(replay.ResponseSnapshot, &value); err != nil {
			return Recovery{}, false, oops.In("coupon_recovery_repository").Code("coupon.idempotency_snapshot_decode_failed").Wrap(err)
		}
		return value, true, nil
	}
	value, err := findRecovery(ctx, tx, id, false)
	return value, true, err
}

func commitReplay(ctx context.Context, tx pgx.Tx, value Recovery, cause error, committed *bool) (Recovery, error) {
	if cause != nil {
		return Recovery{}, cause
	}
	if err := tx.Commit(ctx); err != nil {
		return Recovery{}, dbError("commit_replay", err)
	}
	*committed = true
	return value, nil
}

func (r *PostgresRepository) ready() error {
	if r == nil || r.pool == nil {
		return oops.In("coupon_recovery_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	return nil
}

func dbError(operation string, err error) error {
	return oops.In("coupon_recovery_repository").Code("coupon.database_failed").With("operation", operation).Wrap(err)
}
