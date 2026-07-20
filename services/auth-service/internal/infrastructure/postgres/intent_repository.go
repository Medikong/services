package postgres

import (
	"context"
	"errors"

	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type IntentRepository struct {
	tx pgx.Tx
}

func NewIntentRepository(tx pgx.Tx) *IntentRepository {
	return &IntentRepository{tx: tx}
}

func (r *IntentRepository) Create(ctx context.Context, params domainintent.CreateParams) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_authentication_intents (
			intent_id, client_channel, return_path, intent_type, action_context,
			owner_proof_hash, csrf_secret_hash, csrf_key_version, action_payload_id, expires_at, created_at, updated_at
		) VALUES ($1, $2::varchar, $3, $4, $5, $6, $7, CASE WHEN $2::varchar = 'web' THEN 1 ELSE NULL END, $8, $9, now(), now())
	`, params.ID, params.Channel, params.ReturnPath, params.Type, params.ActionContext, params.OwnerProofHash, params.CSRFHash, params.ActionPayloadID, params.ExpiresAt)
	return err
}

func (r *IntentRepository) CreateActionPayload(ctx context.Context, payload domainintent.ActionPayload) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_action_intent_payloads (action_payload_id, action_name, schema_version, payload_ciphertext, payload_key_id, expires_at, created_at)
		VALUES ($1, $2, 1, $3, 'auth-replay-v1', $4, now())
	`, payload.ID, payload.ActionName, payload.Ciphertext, payload.ExpiresAt)
	return err
}

func (r *IntentRepository) BindActionPayload(ctx context.Context, intentID, payloadID uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `UPDATE auth_action_intent_payloads SET intent_id = $2 WHERE action_payload_id = $1 AND intent_id IS NULL`, payloadID, intentID)
	return err
}

func (r *IntentRepository) FindConsumedActionForUpdate(ctx context.Context, intentID, sessionID uuid.UUID) (domainintent.Intent, domainintent.ActionPayload, error) {
	var current domainintent.Intent
	var payload domainintent.ActionPayload
	err := r.tx.QueryRow(ctx, `
		SELECT i.intent_id, i.client_channel, i.return_path, i.intent_type, i.action_context,
			i.owner_proof_hash, i.csrf_secret_hash, i.expires_at, i.status, i.remember_me,
			p.action_payload_id, p.intent_id, p.action_name, p.payload_ciphertext, p.expires_at, p.delivered_at
		FROM auth_authentication_intents i JOIN auth_action_intent_payloads p ON p.action_payload_id = i.action_payload_id
		WHERE i.intent_id = $1 AND i.status = 'consumed' AND i.consumed_by_session_id = $2
			AND i.expires_at > now() AND p.destroyed_at IS NULL AND p.expires_at > now()
		FOR UPDATE OF i, p
	`, intentID, sessionID).Scan(&current.ID, &current.Channel, &current.ReturnPath, &current.Type, &current.ActionContext, &current.OwnerProofHash, &current.CSRFHash, &current.ExpiresAt, &current.Status, &current.RememberMe, &payload.ID, &payload.IntentID, &payload.ActionName, &payload.Ciphertext, &payload.ExpiresAt, &payload.DeliveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainintent.Intent{}, domainintent.ActionPayload{}, domainintent.ErrNotFound
	}
	if err != nil {
		return domainintent.Intent{}, domainintent.ActionPayload{}, err
	}
	current.ExpiresAt = current.ExpiresAt.UTC()
	payload.ExpiresAt = payload.ExpiresAt.UTC()
	payload.Ciphertext = append([]byte(nil), payload.Ciphertext...)
	return current, payload, nil
}

func (r *IntentRepository) MarkActionDelivered(ctx context.Context, payloadID uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `UPDATE auth_action_intent_payloads SET delivered_at=COALESCE(delivered_at,now()) WHERE action_payload_id=$1 AND delivered_at IS NULL`, payloadID)
	return err
}

func (r *IntentRepository) FindActiveForUpdate(ctx context.Context, id uuid.UUID) (domainintent.Intent, error) {
	var result domainintent.Intent
	err := r.tx.QueryRow(ctx, `
		SELECT intent_id, client_channel, return_path, intent_type, action_context,
			owner_proof_hash, csrf_secret_hash, expires_at, status, remember_me
		FROM auth_authentication_intents
		WHERE intent_id = $1 AND status = 'active' AND expires_at > now()
		FOR UPDATE
	`, id).Scan(
		&result.ID, &result.Channel, &result.ReturnPath, &result.Type, &result.ActionContext,
		&result.OwnerProofHash, &result.CSRFHash, &result.ExpiresAt, &result.Status, &result.RememberMe,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainintent.Intent{}, domainintent.ErrNotFound
	}
	return result, err
}

func (r *IntentRepository) FindCompletionReplayForUpdate(ctx context.Context, id, sessionID uuid.UUID) (domainintent.Intent, error) {
	var result domainintent.Intent
	err := r.tx.QueryRow(ctx, `
		SELECT intent_id, client_channel, return_path, intent_type, action_context,
			owner_proof_hash, csrf_secret_hash, expires_at, status, remember_me
		FROM auth_authentication_intents
		WHERE intent_id = $1 AND status = 'consumed' AND consumed_by_session_id = $2
		FOR UPDATE
	`, id, sessionID).Scan(
		&result.ID, &result.Channel, &result.ReturnPath, &result.Type, &result.ActionContext,
		&result.OwnerProofHash, &result.CSRFHash, &result.ExpiresAt, &result.Status, &result.RememberMe,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainintent.Intent{}, domainintent.ErrNotFound
	}
	return result, err
}

func (r *IntentRepository) RotateOwnerProof(ctx context.Context, id uuid.UUID, ownerProofHash, csrfHash []byte) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_authentication_intents
		SET owner_proof_hash = $2, csrf_secret_hash = $3, row_version = row_version + 1, updated_at = now()
		WHERE intent_id = $1 AND status = 'active' AND expires_at > now()
	`, id, ownerProofHash, csrfHash)
	return err
}

func (r *IntentRepository) SetRememberMe(ctx context.Context, id uuid.UUID, rememberMe bool) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_authentication_intents
		SET remember_me = $2, row_version = row_version + 1, updated_at = now()
		WHERE intent_id = $1 AND status = 'active' AND expires_at > now()
	`, id, rememberMe)
	return err
}

func (r *IntentRepository) Consume(ctx context.Context, id, sessionID uuid.UUID, reason string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_authentication_intents
		SET status = 'consumed', consumed_at = now(), consumed_by_session_id = $2,
			consumption_reason = $3, row_version = row_version + 1, updated_at = now()
		WHERE intent_id = $1 AND status = 'active'
	`, id, sessionID, reason)
	return err
}
