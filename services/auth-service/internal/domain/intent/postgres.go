package intent

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("authentication intent not found")

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, tx pgx.Tx, params CreateParams) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_authentication_intents (
			intent_id, client_channel, return_path, intent_type, action_context,
			owner_proof_hash, csrf_secret_hash, csrf_key_version, action_payload_id, expires_at, created_at, updated_at
		) VALUES ($1, $2::varchar, $3, $4, $5, $6, $7, CASE WHEN $2::varchar = 'web' THEN 1 ELSE NULL END, $8, $9, now(), now())
	`, params.ID, params.Channel, params.ReturnPath, params.Type, params.ActionContext, params.OwnerProofHash, params.CSRFHash, params.ActionPayloadID, params.ExpiresAt)
	return err
}

func (r *PostgresRepository) CreateActionPayload(ctx context.Context, tx pgx.Tx, payload ActionPayload) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_action_intent_payloads (action_payload_id, action_name, schema_version, payload_ciphertext, payload_key_id, expires_at, created_at)
		VALUES ($1, $2, 1, $3, 'auth-replay-v1', $4, now())
	`, payload.ID, payload.ActionName, payload.Ciphertext, payload.ExpiresAt)
	return err
}

func (r *PostgresRepository) BindActionPayload(ctx context.Context, tx pgx.Tx, intentID, payloadID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE auth_action_intent_payloads SET intent_id = $2 WHERE action_payload_id = $1 AND intent_id IS NULL`, payloadID, intentID)
	return err
}

func (r *PostgresRepository) FindConsumedActionForUpdate(ctx context.Context, tx pgx.Tx, intentID, sessionID uuid.UUID) (Intent, ActionPayload, error) {
	var current Intent
	var payload ActionPayload
	err := tx.QueryRow(ctx, `
		SELECT i.intent_id, i.client_channel, i.return_path, i.intent_type, i.action_context,
			i.owner_proof_hash, i.csrf_secret_hash, i.expires_at, i.status, i.remember_me,
			p.action_payload_id, p.intent_id, p.action_name, p.payload_ciphertext, p.expires_at, p.delivered_at
		FROM auth_authentication_intents i JOIN auth_action_intent_payloads p ON p.action_payload_id = i.action_payload_id
		WHERE i.intent_id = $1 AND i.status = 'consumed' AND i.consumed_by_session_id = $2
			AND i.expires_at > now() AND p.destroyed_at IS NULL AND p.expires_at > now()
		FOR UPDATE OF i, p
	`, intentID, sessionID).Scan(&current.ID, &current.Channel, &current.ReturnPath, &current.Type, &current.ActionContext, &current.OwnerProofHash, &current.CSRFHash, &current.ExpiresAt, &current.Status, &current.RememberMe, &payload.ID, &payload.IntentID, &payload.ActionName, &payload.Ciphertext, &payload.ExpiresAt, &payload.DeliveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Intent{}, ActionPayload{}, ErrNotFound
	}
	if err != nil {
		return Intent{}, ActionPayload{}, err
	}
	current.ExpiresAt = current.ExpiresAt.UTC()
	payload.ExpiresAt = payload.ExpiresAt.UTC()
	payload.Ciphertext = append([]byte(nil), payload.Ciphertext...)
	return current, payload, nil
}

func (r *PostgresRepository) MarkActionDelivered(ctx context.Context, tx pgx.Tx, payloadID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE auth_action_intent_payloads SET delivered_at=COALESCE(delivered_at,now()) WHERE action_payload_id=$1 AND delivered_at IS NULL`, payloadID)
	return err
}

func (r *PostgresRepository) FindActiveForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Intent, error) {
	var result Intent
	err := tx.QueryRow(ctx, `
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
		return Intent{}, ErrNotFound
	}
	return result, err
}

func (r *PostgresRepository) FindCompletionReplayForUpdate(ctx context.Context, tx pgx.Tx, id, sessionID uuid.UUID) (Intent, error) {
	var result Intent
	err := tx.QueryRow(ctx, `
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
		return Intent{}, ErrNotFound
	}
	return result, err
}

func (r *PostgresRepository) RotateOwnerProof(ctx context.Context, tx pgx.Tx, id uuid.UUID, ownerProofHash, csrfHash []byte) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_authentication_intents
		SET owner_proof_hash = $2, csrf_secret_hash = $3, row_version = row_version + 1, updated_at = now()
		WHERE intent_id = $1 AND status = 'active' AND expires_at > now()
	`, id, ownerProofHash, csrfHash)
	return err
}

func (r *PostgresRepository) SetRememberMe(ctx context.Context, tx pgx.Tx, id uuid.UUID, rememberMe bool) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_authentication_intents
		SET remember_me = $2, row_version = row_version + 1, updated_at = now()
		WHERE intent_id = $1 AND status = 'active' AND expires_at > now()
	`, id, rememberMe)
	return err
}

func (r *PostgresRepository) Consume(ctx context.Context, tx pgx.Tx, id, sessionID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_authentication_intents
		SET status = 'consumed', consumed_at = now(), consumed_by_session_id = $2,
			consumption_reason = $3, row_version = row_version + 1, updated_at = now()
		WHERE intent_id = $1 AND status = 'active'
	`, id, sessionID, reason)
	return err
}
