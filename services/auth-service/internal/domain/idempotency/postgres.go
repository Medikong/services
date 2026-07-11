package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("idempotency record not found")

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) FindForUpdate(ctx context.Context, tx pgx.Tx, operation string, scopeHash, keyHash []byte) (Record, error) {
	var result Record
	err := tx.QueryRow(ctx, `
		SELECT idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_id, replay_payload_id, expires_at
		FROM auth_idempotency_records
		WHERE operation = $1 AND scope_hash = $2 AND key_hash = $3
		FOR UPDATE
	`, operation, scopeHash, keyHash).Scan(
		&result.ID, &result.Operation, &result.ScopeHash, &result.KeyHash, &result.RequestHash,
		&result.Status, &result.ResourceID, &result.ReplayID, &result.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return result, err
}

func (r *PostgresRepository) CreateProcessing(ctx context.Context, tx pgx.Tx, record Record, resourceType string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_idempotency_records (
			idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_type, resource_id, replay_payload_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7, $8, $9, now())
	`, record.ID, record.Operation, record.ScopeHash, record.KeyHash, record.RequestHash,
		resourceType, record.ResourceID, record.ReplayID, record.ExpiresAt)
	return err
}

// ClaimProcessing atomically claims a key. When another transaction already
// owns the key, PostgreSQL waits for it and then returns the durable record so
// callers can replay its result instead of executing the command twice.
func (r *PostgresRepository) ClaimProcessing(ctx context.Context, tx pgx.Tx, record Record, resourceType string) (Record, bool, error) {
	result, err := tx.Exec(ctx, `
		INSERT INTO auth_idempotency_records (
			idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_type, resource_id, replay_payload_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7, $8, $9, now())
		ON CONFLICT (operation, scope_hash, key_hash) DO NOTHING
	`, record.ID, record.Operation, record.ScopeHash, record.KeyHash, record.RequestHash,
		resourceType, record.ResourceID, record.ReplayID, record.ExpiresAt)
	if err != nil {
		return Record{}, false, err
	}
	if result.RowsAffected() == 1 {
		return record, true, nil
	}
	existing, err := r.FindForUpdate(ctx, tx, record.Operation, record.ScopeHash, record.KeyHash)
	return existing, false, err
}

func (r *PostgresRepository) CreateCompleted(ctx context.Context, tx pgx.Tx, record Record, resourceType, resultCode string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_idempotency_records (
			idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_type, resource_id, result_code, replay_payload_id, expires_at, created_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, 'completed', $6, $7, $8, $9, $10, now(), now())
	`, record.ID, record.Operation, record.ScopeHash, record.KeyHash, record.RequestHash,
		resourceType, record.ResourceID, resultCode, record.ReplayID, record.ExpiresAt)
	return err
}

func (r *PostgresRepository) AttachReplayPayload(ctx context.Context, tx pgx.Tx, recordID, replayID uuid.UUID) error {
	result, err := tx.Exec(ctx, `
		UPDATE auth_idempotency_records
		SET replay_payload_id = $2
		WHERE idempotency_record_id = $1 AND status = 'processing' AND replay_payload_id IS NULL
	`, recordID, replayID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("idempotency replay payload could not be attached")
	}
	return nil
}

func (r *PostgresRepository) Complete(ctx context.Context, tx pgx.Tx, id uuid.UUID, resultCode string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_idempotency_records
		SET status = 'completed', result_code = $2, completed_at = now()
		WHERE idempotency_record_id = $1 AND status = 'processing'
	`, id, resultCode)
	return err
}

func (r *PostgresRepository) CreateReplayPayload(ctx context.Context, tx pgx.Tx, payload ReplayPayload) error {
	if payload.ID == uuid.Nil || payload.Kind == "" || len(payload.Ciphertext) == 0 || len(payload.BindingHash) != 32 || payload.ExpiresAt.IsZero() {
		return errors.New("invalid idempotency replay payload")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_idempotency_replay_payloads (
			replay_payload_id, payload_kind, payload_ciphertext, payload_key_id,
			binding_hash, expires_at, created_at
		) VALUES ($1, $2, $3, 'local-replay-v1', $4, $5, now())
	`, payload.ID, payload.Kind, payload.Ciphertext, payload.BindingHash, payload.ExpiresAt)
	return err
}

func (r *PostgresRepository) FindReplayPayloadForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (ReplayPayload, error) {
	var payload ReplayPayload
	err := tx.QueryRow(ctx, `
		SELECT replay_payload_id, payload_kind, payload_ciphertext, binding_hash,
			replay_count, expires_at, destroyed_at
		FROM auth_idempotency_replay_payloads
		WHERE replay_payload_id = $1
		FOR UPDATE
	`, id).Scan(
		&payload.ID, &payload.Kind, &payload.Ciphertext, &payload.BindingHash,
		&payload.ReplayCount, &payload.ExpiresAt, &payload.DestroyedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReplayPayload{}, ErrNotFound
	}
	return payload, err
}

func (r *PostgresRepository) RecordReplay(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_idempotency_replay_payloads
		SET replay_count = replay_count + 1
		WHERE replay_payload_id = $1 AND destroyed_at IS NULL AND expires_at > now()
	`, id)
	return err
}

func (r *PostgresRepository) DestroyReplayPayload(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_idempotency_replay_payloads
		SET payload_ciphertext = NULL, destroyed_at = COALESCE(destroyed_at, now())
		WHERE replay_payload_id = $1
	`, id)
	return err
}

func NewRecord(operation string, scopeHash, keyHash, requestHash []byte, resourceID *uuid.UUID, replayID *uuid.UUID, expiresAt time.Time) Record {
	return Record{
		ID:          uuid.New(),
		Operation:   operation,
		ScopeHash:   scopeHash,
		KeyHash:     keyHash,
		RequestHash: requestHash,
		ResourceID:  resourceID,
		ReplayID:    replayID,
		ExpiresAt:   expiresAt,
	}
}
