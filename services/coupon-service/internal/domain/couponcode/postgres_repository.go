package couponcode

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

const aggregateType = "CouponCodeBatch"

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) (*PostgresRepository, error) {
	if db == nil {
		return nil, oops.In("coupon_code_repository").Code("coupon_code.database_required").New("postgres pool is required")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) FindByHash(ctx context.Context, hash []byte) (Code, error) {
	if len(hash) != sha256.Size {
		return Code{}, ErrNotFound
	}
	return loadCodeByHash(ctx, r.db, hash, false)
}

func (r *PostgresRepository) Reject(ctx context.Context, hash []byte, userID, issueRequestID, reasonCode string, command Command) (Mutation, error) {
	if len(hash) != sha256.Size || strings.TrimSpace(userID) == "" || strings.TrimSpace(issueRequestID) == "" || strings.TrimSpace(reasonCode) == "" {
		return Mutation{}, ErrInvalidTransition
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		code, err := loadCodeByHash(ctx, tx, hash, true)
		if err != nil {
			return Mutation{}, err
		}
		idem, err := acquireIdempotency(ctx, tx, command, code.BatchID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		batch, err := loadBatch(ctx, tx, code.BatchID, true)
		if err != nil {
			return Mutation{}, err
		}
		resultRef := strings.Join([]string{"code", code.ID, "rejected", issueRequestID}, ":")
		payload, err := json.Marshal(map[string]any{
			"codeId": code.ID, "codeBatchId": code.BatchID, "campaignId": code.CampaignID,
			"issueRequestId": issueRequestID, "userId": userID, "reasonCode": reasonCode, "resultRef": resultRef,
		})
		if err != nil {
			return Mutation{}, oops.In("coupon_code_repository").Code("coupon_code.rejection_encode_failed").Wrap(err)
		}
		mutation := Mutation{
			Code: code, BatchVersion: batch.Version, ResultRef: resultRef,
			ResponseSnapshot: payload, Rejected: true, ReasonCode: reasonCode,
		}
		if err := insertOutbox(ctx, tx, command, "coupon.code.rejected", "EVT.A.19-15", code.BatchID, batch.Version, payload); err != nil {
			return Mutation{}, err
		}
		if err := finishIdempotency(ctx, tx, command, "failed_final", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

// FindByIDWithBatchVersion returns the code plus the version of its owning
// CouponCodeBatch for a correlated confirm/release command.
func (r *PostgresRepository) FindByIDWithBatchVersion(ctx context.Context, codeID string) (Code, int64, error) {
	code, err := loadCodeByID(ctx, r.db, codeID, false)
	if err != nil {
		return Code{}, 0, err
	}
	batch, err := loadBatch(ctx, r.db, code.BatchID, false)
	if err != nil {
		return Code{}, 0, err
	}
	return code, batch.Version, nil
}

func (r *PostgresRepository) Reserve(ctx context.Context, hash []byte, userID, issueRequestID string, reservedUntil time.Time, command Command) (Mutation, error) {
	if len(hash) != sha256.Size || strings.TrimSpace(userID) == "" || issueRequestID == "" || !reservedUntil.After(command.OccurredAt) {
		return Mutation{}, ErrInvalidTransition
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		code, err := loadCodeByHash(ctx, tx, hash, true)
		if err != nil {
			return Mutation{}, err
		}
		idem, err := acquireIdempotency(ctx, tx, command, code.BatchID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		batch, err := loadBatch(ctx, tx, code.BatchID, true)
		if err != nil {
			return Mutation{}, err
		}
		if code.Status == CodeReserved && code.ReservedIssueRequestID == issueRequestID {
			mutation := replayMutation(code, batch.Version)
			if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		if batch.Status != BatchActive || code.Status != CodeAvailable {
			payload, _ := json.Marshal(map[string]any{"codeId": code.ID, "codeBatchId": code.BatchID, "campaignId": code.CampaignID, "reasonCode": "code_unavailable"})
			mutation := Mutation{Code: code, BatchVersion: batch.Version, ResultRef: strings.Join([]string{"code", code.ID, "rejected"}, ":"), ResponseSnapshot: payload, Rejected: true, ReasonCode: "code_unavailable"}
			if err := insertOutbox(ctx, tx, command, "coupon.code.rejected", "EVT.A.19-15", code.BatchID, batch.Version, payload); err != nil {
				return Mutation{}, err
			}
			if err := finishIdempotency(ctx, tx, command, "failed_final", mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		updated, err := code.Reserve(issueRequestID, reservedUntil, command.OccurredAt)
		if err != nil {
			return Mutation{}, err
		}
		updated.Version = code.Version + 1
		tag, err := tx.Exec(ctx, `UPDATE coupon_codes SET status='reserved',reserved_issue_request_id=$1,reserved_until=$2,version=$3,updated_at=$4 WHERE code_id=$5 AND status='available' AND version=$6`, issueRequestID, reservedUntil, updated.Version, command.OccurredAt, code.ID, code.Version)
		if err != nil {
			return Mutation{}, dbError("reserve_code", err)
		}
		if tag.RowsAffected() != 1 {
			return Mutation{}, ErrInvalidTransition
		}
		newBatchVersion, err := bumpBatch(ctx, tx, batch.ID, batch.Version, command.OccurredAt)
		if err != nil {
			return Mutation{}, err
		}
		payload := reservationPayload(updated, userID)
		mutation := Mutation{Code: updated, BatchVersion: newBatchVersion, ResultRef: strings.Join([]string{"code", code.ID, "reserved", issueRequestID}, ":"), ResponseSnapshot: payload}
		if err := insertOutbox(ctx, tx, command, "coupon.code.validated", "EVT.A.19-12", batch.ID, newBatchVersion, payload); err != nil {
			return Mutation{}, err
		}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

func (r *PostgresRepository) Confirm(ctx context.Context, codeID, issueRequestID, userCouponID string, expectedBatchVersion int64, command Command) (Mutation, error) {
	if codeID == "" || issueRequestID == "" || userCouponID == "" {
		return Mutation{}, ErrInvalidTransition
	}
	return r.decide(ctx, codeID, issueRequestID, userCouponID, expectedBatchVersion, CodeRedeemed, command)
}

func (r *PostgresRepository) Release(ctx context.Context, codeID, issueRequestID string, expectedBatchVersion int64, command Command) (Mutation, error) {
	if codeID == "" || issueRequestID == "" {
		return Mutation{}, ErrInvalidTransition
	}
	return r.decide(ctx, codeID, issueRequestID, "", expectedBatchVersion, CodeAvailable, command)
}

func (r *PostgresRepository) decide(ctx context.Context, codeID, issueRequestID, userCouponID string, expectedBatchVersion int64, target CodeStatus, command Command) (Mutation, error) {
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		code, err := loadCodeByID(ctx, tx, codeID, true)
		if err != nil {
			return Mutation{}, err
		}
		idem, err := acquireIdempotency(ctx, tx, command, code.BatchID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		batch, err := loadBatch(ctx, tx, code.BatchID, true)
		if err != nil {
			return Mutation{}, err
		}
		if batch.Version != expectedBatchVersion {
			return Mutation{}, ErrVersionConflict
		}
		if (target == CodeRedeemed && code.Status == CodeRedeemed && code.ReservedIssueRequestID == issueRequestID && code.RedeemedUserCouponID == userCouponID) || (target == CodeAvailable && code.Status == CodeAvailable && code.ReservedIssueRequestID == "") {
			mutation := replayMutation(code, batch.Version)
			if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
				return Mutation{}, err
			}
			return mutation, nil
		}
		updated := Code{}
		if target == CodeRedeemed {
			updated, err = code.Redeem(issueRequestID, userCouponID, command.OccurredAt)
		} else {
			updated, err = code.Release(issueRequestID)
		}
		if err != nil {
			return Mutation{}, err
		}
		updated.Version = code.Version + 1
		var rowsAffected int64
		if target == CodeRedeemed {
			tag, execErr := tx.Exec(ctx, `UPDATE coupon_codes SET status='redeemed',redeemed_user_coupon_id=$1,redeemed_at=$2,version=$3,updated_at=$2 WHERE code_id=$4 AND status='reserved' AND reserved_issue_request_id=$5 AND version=$6`, userCouponID, command.OccurredAt, updated.Version, code.ID, issueRequestID, code.Version)
			err = execErr
			rowsAffected = tag.RowsAffected()
		} else {
			tag, execErr := tx.Exec(ctx, `UPDATE coupon_codes SET status='available',reserved_issue_request_id=NULL,reserved_until=NULL,version=$1,updated_at=$2 WHERE code_id=$3 AND status='reserved' AND reserved_issue_request_id=$4 AND version=$5`, updated.Version, command.OccurredAt, code.ID, issueRequestID, code.Version)
			err = execErr
			rowsAffected = tag.RowsAffected()
		}
		if err != nil {
			return Mutation{}, dbError("decide_code", err)
		}
		if rowsAffected != 1 {
			return Mutation{}, ErrInvalidTransition
		}
		newBatchVersion, err := bumpBatch(ctx, tx, batch.ID, expectedBatchVersion, command.OccurredAt)
		if err != nil {
			return Mutation{}, err
		}
		payload := codePayload(updated)
		eventType, documentID, resultWord := "coupon.code.released", "EVT.A.19-14", "released"
		if target == CodeRedeemed {
			eventType, documentID, resultWord = "coupon.code.redeemed", "EVT.A.19-13", "redeemed"
		}
		mutation := Mutation{Code: updated, BatchVersion: newBatchVersion, ResultRef: strings.Join([]string{"code", code.ID, resultWord}, ":"), ResponseSnapshot: payload}
		if err := insertOutbox(ctx, tx, command, eventType, documentID, batch.ID, newBatchVersion, payload); err != nil {
			return Mutation{}, err
		}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadCodeByHash(ctx context.Context, db queryer, hash []byte, lock bool) (Code, error) {
	return loadCode(ctx, db, `code_hash=$1`, hash, lock)
}

func loadCodeByID(ctx context.Context, db queryer, codeID string, lock bool) (Code, error) {
	return loadCode(ctx, db, `code_id=$1`, codeID, lock)
}

func loadCode(ctx context.Context, db queryer, condition string, value any, lock bool) (Code, error) {
	query := `SELECT code_id,code_batch_id,campaign_id,code_hash,hash_version,normalization_version,COALESCE(code_suffix,''),status,COALESCE(reserved_issue_request_id,''),reserved_until,COALESCE(redeemed_user_coupon_id,''),redeemed_at,version,created_at,updated_at FROM coupon_codes WHERE ` + condition
	if lock {
		query += ` FOR UPDATE`
	}
	var code Code
	var reservedUntil, redeemedAt sql.NullTime
	err := db.QueryRow(ctx, query, value).Scan(&code.ID, &code.BatchID, &code.CampaignID, &code.Hash, &code.HashAlgorithmVersion, &code.NormalizationVersion, &code.Suffix, &code.Status, &code.ReservedIssueRequestID, &reservedUntil, &code.RedeemedUserCouponID, &redeemedAt, &code.Version, &code.CreatedAt, &code.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Code{}, ErrNotFound
	}
	if err != nil {
		return Code{}, dbError("get_code", err)
	}
	if reservedUntil.Valid {
		code.ReservedUntil = &reservedUntil.Time
	}
	if redeemedAt.Valid {
		code.RedeemedAt = &redeemedAt.Time
	}
	return code, nil
}

func loadBatch(ctx context.Context, db queryer, batchID string, lock bool) (Batch, error) {
	query := `SELECT code_batch_id,campaign_id,status,format,quantity,created_count,COALESCE(distribution_channel,''),creator_ref,version,created_at,updated_at FROM coupon_code_batches WHERE code_batch_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var batch Batch
	err := db.QueryRow(ctx, query, batchID).Scan(&batch.ID, &batch.CampaignID, &batch.Status, &batch.Format, &batch.Quantity, &batch.CreatedCount, &batch.DistributionChannel, &batch.CreatorRef, &batch.Version, &batch.CreatedAt, &batch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, dbError("get_batch", err)
	}
	return batch, nil
}

func bumpBatch(ctx context.Context, tx pgx.Tx, batchID string, expectedVersion int64, at time.Time) (int64, error) {
	newVersion := expectedVersion + 1
	tag, err := tx.Exec(ctx, `UPDATE coupon_code_batches SET version=$1,updated_at=$2 WHERE code_batch_id=$3 AND version=$4`, newVersion, at, batchID, expectedVersion)
	if err != nil {
		return 0, dbError("bump_batch_version", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, ErrVersionConflict
	}
	return newVersion, nil
}

type idempotencyResult struct {
	mutation Mutation
	replayed bool
}

func acquireIdempotency(ctx context.Context, tx pgx.Tx, command Command, ownerID string) (idempotencyResult, error) {
	if command.OperationType == "" || command.BusinessKey == "" || command.RequestHash == "" || command.CorrelationID == "" || command.OccurredAt.IsZero() ||
		!command.LeaseUntil.After(command.OccurredAt) || !command.ExpiresAt.After(command.LeaseUntil) {
		return idempotencyResult{}, oops.In("coupon_code_repository").Code("coupon_code.command_invalid").New("command idempotency, correlation, lease, and expiry fields are required")
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
	var response struct {
		CodeID         string     `json:"codeId"`
		CodeBatchID    string     `json:"codeBatchId"`
		CampaignID     string     `json:"campaignId"`
		Status         CodeStatus `json:"status"`
		IssueRequestID string     `json:"issueRequestId"`
		UserCouponID   string     `json:"userCouponId"`
		ReservedUntil  *time.Time `json:"reservedUntil"`
		ReasonCode     string     `json:"reasonCode"`
	}
	if err := json.Unmarshal(snapshot, &response); err != nil {
		return idempotencyResult{}, oops.In("coupon_code_repository").Code("coupon_code.snapshot_decode_failed").Wrap(err)
	}
	mutation := Mutation{
		Code: Code{
			ID: response.CodeID, BatchID: response.CodeBatchID, CampaignID: response.CampaignID,
			Status: response.Status, ReservedIssueRequestID: response.IssueRequestID,
			RedeemedUserCouponID: response.UserCouponID, ReservedUntil: response.ReservedUntil,
		},
		ResultRef: resultRef.String, ResponseSnapshot: snapshot, Replayed: true,
		Rejected: status == "failed_final", ReasonCode: response.ReasonCode,
	}
	return idempotencyResult{mutation: mutation, replayed: true}, nil
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

func codePayload(code Code) []byte {
	payload, _ := json.Marshal(map[string]any{
		"codeId": code.ID, "codeBatchId": code.BatchID, "campaignId": code.CampaignID,
		"status": code.Status, "issueRequestId": code.ReservedIssueRequestID,
		"userCouponId": code.RedeemedUserCouponID, "reservedUntil": code.ReservedUntil,
	})
	return payload
}

func reservationPayload(code Code, userID string) []byte {
	payload, _ := json.Marshal(map[string]any{
		"codeId": code.ID, "codeBatchId": code.BatchID, "campaignId": code.CampaignID,
		"status": code.Status, "issueRequestId": code.ReservedIssueRequestID,
		"userId": userID, "reservedUntil": code.ReservedUntil,
	})
	return payload
}

func replayMutation(code Code, batchVersion int64) Mutation {
	return Mutation{Code: code, BatchVersion: batchVersion, ResultRef: strings.Join([]string{"code", code.ID, string(code.Status)}, ":"), ResponseSnapshot: codePayload(code), Replayed: true}
}

func dbError(operation string, err error) error {
	return oops.In("coupon_code_repository").Code("coupon_code.database_failed").With("operation", operation).Wrap(err)
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
