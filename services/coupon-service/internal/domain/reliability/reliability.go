package reliability

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

type Command struct {
	DocumentID    string
	OperationType string
	BusinessKey   string
	RequestHash   [32]byte
	CorrelationID string
	CausationID   string
	TraceID       string
	LeaseUntil    time.Time
	ExpiresAt     time.Time
}

type Replay struct {
	Existing         bool
	Resume           bool
	Status           string
	ResultRef        string
	ResponseSnapshot json.RawMessage
}

func Claim(ctx context.Context, tx pgx.Tx, command Command, ownerType, ownerID string) (Replay, error) {
	if command.LeaseUntil.IsZero() || !command.ExpiresAt.After(command.LeaseUntil) {
		return Replay{}, oops.In("coupon_idempotency").Code("coupon.idempotency_deadline_required").New("idempotency lease and later expiry are required")
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO coupon_idempotency_records (
			operation_type, business_key, owner_type, owner_id, request_hash,
			status, locked_until, expires_at
		) VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7)
		ON CONFLICT (operation_type, business_key) DO NOTHING
	`, command.OperationType, command.BusinessKey, ownerType, ownerID, command.RequestHash[:], command.LeaseUntil, command.ExpiresAt)
	if err != nil {
		return Replay{}, oops.In("coupon_idempotency").Code("coupon.idempotency_claim_failed").Wrap(err)
	}
	if result.RowsAffected() == 1 {
		return Replay{}, nil
	}

	var storedHash []byte
	var replay Replay
	var snapshot []byte
	var lockedUntil *time.Time
	err = tx.QueryRow(ctx, `
		SELECT request_hash, status, COALESCE(result_ref, ''), response_snapshot, locked_until
		FROM coupon_idempotency_records
		WHERE operation_type = $1 AND business_key = $2
		FOR UPDATE
	`, command.OperationType, command.BusinessKey).Scan(&storedHash, &replay.Status, &replay.ResultRef, &snapshot, &lockedUntil)
	if err != nil {
		return Replay{}, oops.In("coupon_idempotency").Code("coupon.idempotency_read_failed").Wrap(err)
	}
	if !bytes.Equal(storedHash, command.RequestHash[:]) {
		return Replay{}, oops.In("coupon_idempotency").Code("coupon.idempotency_conflict").New("idempotency key was already used with a different request")
	}
	replay.Existing = true
	replay.ResponseSnapshot = append(json.RawMessage(nil), snapshot...)
	if replay.Status != "processing" {
		return replay, nil
	}
	now := time.Now().UTC()
	if lockedUntil != nil && lockedUntil.After(now) {
		return replay, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE coupon_idempotency_records
		SET owner_type = $3, owner_id = $4, locked_until = $5, expires_at = $6, updated_at = now()
		WHERE operation_type = $1 AND business_key = $2 AND status = 'processing'
	`, command.OperationType, command.BusinessKey, ownerType, ownerID, command.LeaseUntil, command.ExpiresAt); err != nil {
		return Replay{}, oops.In("coupon_idempotency").Code("coupon.idempotency_resume_failed").Wrap(err)
	}
	replay.Resume = true
	return replay, nil
}

func Complete(ctx context.Context, tx pgx.Tx, command Command, resultRef string, response any) error {
	snapshot, err := json.Marshal(response)
	if err != nil {
		return oops.In("coupon_idempotency").Code("coupon.idempotency_response_encode_failed").Wrap(err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE coupon_idempotency_records
		SET status = 'completed', result_ref = $3, response_snapshot = $4,
			locked_until = NULL, completed_at = now(), updated_at = now()
		WHERE operation_type = $1 AND business_key = $2 AND request_hash = $5
	`, command.OperationType, command.BusinessKey, resultRef, snapshot, command.RequestHash[:])
	if err != nil {
		return oops.In("coupon_idempotency").Code("coupon.idempotency_complete_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_idempotency").Code("coupon.idempotency_lost").New("idempotency claim is no longer owned by this command")
	}
	return nil
}

type Event struct {
	ID                   uuid.UUID
	DocumentID           string
	Type                 string
	AggregateType        string
	AggregateID          string
	AggregateVersion     int64
	CorrelationID        string
	CausationID          string
	TraceID              string
	PayloadSchemaVersion int16
	Data                 any
	OccurredAt           time.Time
}

func AppendOutbox(ctx context.Context, tx pgx.Tx, event Event) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.PayloadSchemaVersion == 0 {
		event.PayloadSchemaVersion = 1
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return oops.In("coupon_outbox").Code("coupon.outbox_payload_encode_failed").Wrap(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO domain_outbox (
			event_id, event_type, event_document_id, payload_schema_version,
			aggregate_type, aggregate_id, aggregate_version,
			correlation_id, causation_id, trace_id, payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), $11, $12)
	`, event.ID, event.Type, event.DocumentID, event.PayloadSchemaVersion,
		event.AggregateType, event.AggregateID, event.AggregateVersion,
		event.CorrelationID, event.CausationID, event.TraceID, payload, event.OccurredAt)
	if err != nil {
		return oops.In("coupon_outbox").Code("coupon.outbox_append_failed").Wrap(err)
	}
	return nil
}

func Rollback(ctx context.Context, tx pgx.Tx, operation string, cause error) error {
	if tx == nil {
		return cause
	}
	rollbackErr := tx.Rollback(context.WithoutCancel(ctx))
	if rollbackErr != nil && rollbackErr != pgx.ErrTxClosed {
		rollbackErr = oops.In(operation).Code("coupon.transaction_rollback_failed").Wrap(rollbackErr)
	} else {
		rollbackErr = nil
	}
	return oops.Join(cause, rollbackErr)
}
