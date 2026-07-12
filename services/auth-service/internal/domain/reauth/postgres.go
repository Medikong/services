package reauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}
func (r *PostgresRepository) Create(ctx context.Context, tx pgx.Tx, p Proof) error {
	_, err := tx.Exec(ctx, `INSERT INTO auth_reauth_proofs (reauth_proof_id,proof_hash,proof_key_version,user_id,session_id,authenticated_identity_id,authentication_method,purpose,authenticated_at,expires_at,row_version,created_at) VALUES ($1,$2,1,$3,$4,$5,'email_password',$6,now(),$7,0,$8)`, p.ID, p.Hash, p.UserID, p.SessionID, p.IdentityID, p.Purpose, p.ExpiresAt, p.CreatedAt)
	return err
}
func (r *PostgresRepository) FindActiveForUpdate(ctx context.Context, tx pgx.Tx, hash []byte, userID, sessionID uuid.UUID, purpose string) (Proof, error) {
	var p Proof
	var identityID pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT reauth_proof_id,proof_hash,user_id,session_id,authenticated_identity_id,purpose,expires_at,consumed_at,invalidated_at,row_version,created_at FROM auth_reauth_proofs WHERE proof_hash=$1 AND user_id=$2 AND session_id=$3 AND purpose=$4 AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at>now() FOR UPDATE`, hash, userID, sessionID, purpose).Scan(&p.ID, &p.Hash, &p.UserID, &p.SessionID, &identityID, &p.Purpose, &p.ExpiresAt, &p.ConsumedAt, &p.InvalidatedAt, &p.Version, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proof{}, ErrNotFound
	}
	if err != nil {
		return Proof{}, err
	}
	if identityID.Valid {
		id := uuid.UUID(identityID.Bytes)
		p.IdentityID = &id
	}
	p.ExpiresAt = p.ExpiresAt.UTC()
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}
func (r *PostgresRepository) Consume(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE auth_reauth_proofs SET consumed_at=now(),row_version=row_version+1 WHERE reauth_proof_id=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, id)
	return err
}

var _ = time.Now
