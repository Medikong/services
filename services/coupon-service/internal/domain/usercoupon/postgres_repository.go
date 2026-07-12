package usercoupon

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

const aggregateType = "UserCoupon"

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) (*PostgresRepository, error) {
	if db == nil {
		return nil, oops.In("user_coupon_repository").Code("user_coupon.database_required").New("postgres pool is required")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) Grant(ctx context.Context, coupon Coupon, command Command) (Mutation, error) {
	if err := coupon.Validate(); err != nil {
		return Mutation{}, err
	}
	if coupon.Status != StatusGranted {
		return Mutation{}, ErrInvalidTransition
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, coupon.ID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO user_coupons (user_coupon_id,campaign_id,policy_version,user_id,issue_request_id,status,usable_from,expires_at,grant_snapshot,result_ref,version) VALUES ($1,$2,$3,$4,$5,'granted',$6,$7,$8,$9,$10) ON CONFLICT (issue_request_id) DO NOTHING`, coupon.ID, coupon.CampaignID, coupon.PolicyVersion, coupon.UserID, coupon.IssueRequestID, coupon.UsableFrom, coupon.ExpiresAt, coupon.GrantSnapshot, coupon.ResultRef, coupon.Version)
		if err != nil {
			return Mutation{}, dbError("grant_coupon", err)
		}
		if tag.RowsAffected() == 0 {
			existing, err := loadByIssueRequest(ctx, tx, coupon.IssueRequestID, true)
			if err != nil {
				return Mutation{}, err
			}
			if existing.ID != coupon.ID || existing.CampaignID != coupon.CampaignID || existing.UserID != coupon.UserID {
				return Mutation{}, ErrIssueRequestConflict
			}
			mutation := replayMutation(existing)
			if err := finishIdempotency(ctx, tx, command, mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		payload := couponPayload(coupon)
		mutation := Mutation{Coupon: coupon, ResultRef: coupon.ResultRef, ResponseSnapshot: payload}
		if err := insertLedger(ctx, tx, coupon, "coupon.user_coupon.issued", coupon.ResultRef, payload, command.OccurredAt); err != nil {
			return Mutation{}, err
		}
		if err := insertOutbox(ctx, tx, command, "coupon.user_coupon.issued", "EVT.A.19-09", coupon.ID, coupon.Version, payload); err != nil {
			return Mutation{}, err
		}
		if err := finishIdempotency(ctx, tx, command, mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

func (r *PostgresRepository) Get(ctx context.Context, couponID string) (Coupon, error) {
	return loadByID(ctx, r.db, couponID, false)
}

func (r *PostgresRepository) GetByIssueRequest(ctx context.Context, issueRequestID string) (Coupon, error) {
	return loadByIssueRequest(ctx, r.db, issueRequestID, false)
}

func (r *PostgresRepository) FindExpirable(ctx context.Context, asOf time.Time, limit int) ([]Coupon, error) {
	if limit <= 0 {
		return nil, oops.In("user_coupon_repository").Code("user_coupon.limit_invalid").New("expirable coupon limit must be positive")
	}
	rows, err := r.db.Query(ctx, `SELECT user_coupon_id,campaign_id,policy_version,user_id,issue_request_id,status,usable_from,expires_at,grant_snapshot,result_ref,version,created_at,updated_at FROM user_coupons WHERE status='granted' AND expires_at<=$1 ORDER BY expires_at,user_coupon_id LIMIT $2`, asOf, limit)
	if err != nil {
		return nil, dbError("find_expirable", err)
	}
	defer rows.Close()
	var coupons []Coupon
	for rows.Next() {
		var coupon Coupon
		if err := scanCoupon(rows, &coupon); err != nil {
			return nil, err
		}
		coupons = append(coupons, coupon)
	}
	if err := rows.Err(); err != nil {
		return nil, dbError("find_expirable", err)
	}
	return coupons, nil
}

func (r *PostgresRepository) Expire(ctx context.Context, couponID string, expectedVersion int64, asOf time.Time, command Command) (Mutation, error) {
	return r.transition(ctx, couponID, expectedVersion, command, func(current Coupon) (Coupon, error) {
		return current.Expire(asOf)
	}, "coupon.user_coupon.expired", "EVT.A.19-31")
}

func (r *PostgresRepository) transition(ctx context.Context, couponID string, expectedVersion int64, command Command, mutate func(Coupon) (Coupon, error), eventType, documentID string) (Mutation, error) {
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, couponID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		current, err := loadByID(ctx, tx, couponID, true)
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
			if err := finishIdempotency(ctx, tx, command, mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		resultRef := strings.Join([]string{"user_coupon", couponID, string(updated.Status)}, ":")
		updated.ResultRef = resultRef
		tag, err := tx.Exec(ctx, `UPDATE user_coupons SET status=$1,result_ref=$2,version=$3,updated_at=$4 WHERE user_coupon_id=$5 AND version=$6 AND status='granted'`, updated.Status, resultRef, updated.Version, command.OccurredAt, couponID, expectedVersion)
		if err != nil {
			return Mutation{}, dbError("transition_coupon", err)
		}
		if tag.RowsAffected() != 1 {
			return Mutation{}, ErrVersionConflict
		}
		updated.UpdatedAt = command.OccurredAt
		payload := couponPayload(updated)
		mutation := Mutation{Coupon: updated, ResultRef: resultRef, ResponseSnapshot: payload}
		if err := insertLedger(ctx, tx, updated, eventType, mutation.ResultRef, payload, command.OccurredAt); err != nil {
			return Mutation{}, err
		}
		if err := insertOutbox(ctx, tx, command, eventType, documentID, couponID, updated.Version, payload); err != nil {
			return Mutation{}, err
		}
		if err := finishIdempotency(ctx, tx, command, mutation); err != nil {
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

func loadByID(ctx context.Context, db queryer, couponID string, lock bool) (Coupon, error) {
	return loadCoupon(ctx, db, `user_coupon_id=$1`, couponID, lock)
}

func loadByIssueRequest(ctx context.Context, db queryer, issueRequestID string, lock bool) (Coupon, error) {
	return loadCoupon(ctx, db, `issue_request_id=$1`, issueRequestID, lock)
}

func loadCoupon(ctx context.Context, db queryer, condition string, value any, lock bool) (Coupon, error) {
	query := `SELECT user_coupon_id,campaign_id,policy_version,user_id,issue_request_id,status,usable_from,expires_at,grant_snapshot,result_ref,version,created_at,updated_at FROM user_coupons WHERE ` + condition
	if lock {
		query += ` FOR UPDATE`
	}
	var coupon Coupon
	if err := scanCoupon(db.QueryRow(ctx, query, value), &coupon); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Coupon{}, ErrNotFound
		}
		return Coupon{}, err
	}
	return coupon, nil
}

func scanCoupon(row rowScanner, coupon *Coupon) error {
	if err := row.Scan(&coupon.ID, &coupon.CampaignID, &coupon.PolicyVersion, &coupon.UserID, &coupon.IssueRequestID, &coupon.Status, &coupon.UsableFrom, &coupon.ExpiresAt, &coupon.GrantSnapshot, &coupon.ResultRef, &coupon.Version, &coupon.CreatedAt, &coupon.UpdatedAt); err != nil {
		return dbError("scan_coupon", err)
	}
	return nil
}

func insertLedger(ctx context.Context, tx pgx.Tx, coupon Coupon, eventType, resultRef string, payload []byte, occurredAt time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO user_coupon_ledger (ledger_id,user_coupon_id,issue_request_id,event_type,result_ref,payload,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, uuid.New(), coupon.ID, coupon.IssueRequestID, eventType, resultRef, payload, occurredAt)
	if err != nil {
		return dbError("insert_ledger", err)
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
		return idempotencyResult{}, oops.In("user_coupon_repository").Code("user_coupon.command_invalid").New("command idempotency, correlation, lease, and expiry fields are required")
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
	var coupon Coupon
	if err := json.Unmarshal(snapshot, &coupon); err != nil {
		return idempotencyResult{}, oops.In("user_coupon_repository").Code("user_coupon.snapshot_decode_failed").Wrap(err)
	}
	return idempotencyResult{mutation: Mutation{Coupon: coupon, ResultRef: resultRef.String, ResponseSnapshot: snapshot, Replayed: true}, replayed: true}, nil
}

func finishIdempotency(ctx context.Context, tx pgx.Tx, command Command, mutation Mutation) error {
	digest := sha256.Sum256([]byte(command.RequestHash))
	tag, err := tx.Exec(ctx, `UPDATE coupon_idempotency_records SET status='completed',result_ref=$1,response_snapshot=$2,locked_until=NULL,completed_at=$3,updated_at=$3 WHERE operation_type=$4 AND business_key=$5 AND request_hash=$6 AND status='processing'`, mutation.ResultRef, mutation.ResponseSnapshot, command.OccurredAt, command.OperationType, command.BusinessKey, digest[:])
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

func couponPayload(coupon Coupon) []byte {
	payload, _ := json.Marshal(coupon)
	return payload
}

func replayMutation(coupon Coupon) Mutation {
	return Mutation{Coupon: coupon, ResultRef: coupon.ResultRef, ResponseSnapshot: couponPayload(coupon), Replayed: true}
}

func dbError(operation string, err error) error {
	return oops.In("user_coupon_repository").Code("user_coupon.database_failed").With("operation", operation).Wrap(err)
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
