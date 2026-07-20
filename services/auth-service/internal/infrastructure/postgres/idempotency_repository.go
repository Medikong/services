package postgres

import (
	"context"
	"errors"

	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

type IdempotencyRepository struct {
	tx pgx.Tx
}

func NewIdempotencyRepository(tx pgx.Tx) *IdempotencyRepository {
	return &IdempotencyRepository{tx: tx}
}

func (r *IdempotencyRepository) FindForUpdate(ctx context.Context, operation string, scopeHash, keyHash []byte) (domainidempotency.Record, error) {
	var result domainidempotency.Record
	err := r.tx.QueryRow(ctx, `
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
		return domainidempotency.Record{}, domainidempotency.ErrNotFound
	}
	return result, err
}

func (r *IdempotencyRepository) CreateProcessing(ctx context.Context, record domainidempotency.Record, resourceType string) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_idempotency_records (
			idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_type, resource_id, replay_payload_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7, $8, $9, now())
	`, record.ID, record.Operation, record.ScopeHash, record.KeyHash, record.RequestHash,
		resourceType, record.ResourceID, record.ReplayID, record.ExpiresAt)
	return err
}

func (r *IdempotencyRepository) ClaimProcessing(ctx context.Context, record domainidempotency.Record, resourceType string) (domainidempotency.Record, bool, error) {
	result, err := r.tx.Exec(ctx, `
		INSERT INTO auth_idempotency_records (
			idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_type, resource_id, replay_payload_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7, $8, $9, now())
		ON CONFLICT (operation, scope_hash, key_hash) DO NOTHING
	`, record.ID, record.Operation, record.ScopeHash, record.KeyHash, record.RequestHash,
		resourceType, record.ResourceID, record.ReplayID, record.ExpiresAt)
	if err != nil {
		return domainidempotency.Record{}, false, err
	}
	if result.RowsAffected() == 1 {
		return record, true, nil
	}
	existing, err := r.FindForUpdate(ctx, record.Operation, record.ScopeHash, record.KeyHash)
	return existing, false, err
}

func (r *IdempotencyRepository) CreateCompleted(ctx context.Context, record domainidempotency.Record, resourceType, resultCode string) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_idempotency_records (
			idempotency_record_id, operation, scope_hash, key_hash, request_hash, status,
			resource_type, resource_id, result_code, replay_payload_id, expires_at, created_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, 'completed', $6, $7, $8, $9, $10, now(), now())
	`, record.ID, record.Operation, record.ScopeHash, record.KeyHash, record.RequestHash,
		resourceType, record.ResourceID, resultCode, record.ReplayID, record.ExpiresAt)
	return err
}

func (r *IdempotencyRepository) AttachReplayPayload(ctx context.Context, recordID, replayID uuid.UUID) error {
	result, err := r.tx.Exec(ctx, `
		UPDATE auth_idempotency_records
		SET replay_payload_id = $2
		WHERE idempotency_record_id = $1 AND status = 'processing' AND replay_payload_id IS NULL
	`, recordID, replayID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return oops.In("idempotency_repository").Code("idempotency.replay_attach_failed").New("idempotency replay payload could not be attached")
	}
	return nil
}

func (r *IdempotencyRepository) Complete(ctx context.Context, id uuid.UUID, resultCode string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_idempotency_records
		SET status = 'completed', result_code = $2, completed_at = now()
		WHERE idempotency_record_id = $1 AND status = 'processing'
	`, id, resultCode)
	return err
}

func (r *IdempotencyRepository) CreateReplayPayload(ctx context.Context, payload domainidempotency.ReplayPayload) error {
	if payload.ID == uuid.Nil || payload.Kind == "" || len(payload.Ciphertext) == 0 || len(payload.BindingHash) != 32 || payload.ExpiresAt.IsZero() {
		return oops.In("idempotency_repository").Code("idempotency.replay_invalid").New("invalid idempotency replay payload")
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_idempotency_replay_payloads (
			replay_payload_id, payload_kind, payload_ciphertext, payload_key_id,
			binding_hash, expires_at, created_at
		) VALUES ($1, $2, $3, 'local-replay-v1', $4, $5, now())
	`, payload.ID, payload.Kind, payload.Ciphertext, payload.BindingHash, payload.ExpiresAt)
	return err
}

func (r *IdempotencyRepository) FindReplayPayloadForUpdate(ctx context.Context, id uuid.UUID) (domainidempotency.ReplayPayload, error) {
	var payload domainidempotency.ReplayPayload
	err := r.tx.QueryRow(ctx, `
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
		return domainidempotency.ReplayPayload{}, domainidempotency.ErrNotFound
	}
	return payload, err
}

func (r *IdempotencyRepository) RecordReplay(ctx context.Context, id uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_idempotency_replay_payloads
		SET replay_count = replay_count + 1
		WHERE replay_payload_id = $1 AND destroyed_at IS NULL AND expires_at > now()
	`, id)
	return err
}

func (r *IdempotencyRepository) DestroyReplayPayload(ctx context.Context, id uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_idempotency_replay_payloads
		SET payload_ciphertext = NULL, destroyed_at = COALESCE(destroyed_at, now())
		WHERE replay_payload_id = $1
	`, id)
	return err
}
