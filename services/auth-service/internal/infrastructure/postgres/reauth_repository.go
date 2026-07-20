package postgres

import (
	"context"
	"errors"

	applicationreauth "github.com/Medikong/services/services/auth-service/internal/application/reauth"
	domainreauth "github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ReauthenticationRepository struct {
	tx pgx.Tx
}

func NewReauthenticationRepository(tx pgx.Tx) *ReauthenticationRepository {
	return &ReauthenticationRepository{tx: tx}
}

func (r *ReauthenticationRepository) Create(ctx context.Context, proof domainreauth.Proof) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_reauth_proofs (
			reauth_proof_id, proof_hash, proof_key_version, user_id, session_id,
			authenticated_identity_id, authentication_method, purpose, authenticated_at,
			expires_at, row_version, created_at
		) VALUES ($1, $2, 1, $3, $4, $5, 'email_password', $6, now(), $7, 0, $8)
	`, proof.ID, proof.Hash, proof.UserID, proof.SessionID, proof.IdentityID, proof.Purpose, proof.ExpiresAt, proof.CreatedAt)
	return err
}

func (r *ReauthenticationRepository) FindActiveForUpdate(ctx context.Context, hash []byte, userID, sessionID uuid.UUID, purpose string) (domainreauth.Proof, error) {
	var proof domainreauth.Proof
	var identityID pgtype.UUID
	err := r.tx.QueryRow(ctx, `
		SELECT reauth_proof_id, proof_hash, user_id, session_id, authenticated_identity_id,
			purpose, expires_at, consumed_at, invalidated_at, row_version, created_at
		FROM auth_reauth_proofs
		WHERE proof_hash = $1 AND user_id = $2 AND session_id = $3 AND purpose = $4
			AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > now()
		FOR UPDATE
	`, hash, userID, sessionID, purpose).Scan(
		&proof.ID, &proof.Hash, &proof.UserID, &proof.SessionID, &identityID,
		&proof.Purpose, &proof.ExpiresAt, &proof.ConsumedAt, &proof.InvalidatedAt, &proof.Version, &proof.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainreauth.Proof{}, domainreauth.ErrNotFound
	}
	if err != nil {
		return domainreauth.Proof{}, err
	}
	if identityID.Valid {
		value := uuid.UUID(identityID.Bytes)
		proof.IdentityID = &value
	}
	proof.ExpiresAt = proof.ExpiresAt.UTC()
	proof.CreatedAt = proof.CreatedAt.UTC()
	return proof, nil
}

func (r *ReauthenticationRepository) Consume(ctx context.Context, id uuid.UUID) error {
	result, err := r.tx.Exec(ctx, `
		UPDATE auth_reauth_proofs
		SET consumed_at = now(), row_version = row_version + 1
		WHERE reauth_proof_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domainreauth.ErrNotFound
	}
	return nil
}

var _ applicationreauth.Repository = (*ReauthenticationRepository)(nil)
