package passwordreset

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, tx pgx.Tx, value Reset) error {
	if tx == nil {
		return errors.New("password reset transaction is required")
	}
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_password_resets (password_reset_id, intent_id, identity_id, challenge_id, status, reset_grant_hash, reset_grant_key_version, policy_version, expires_at, challenge_verified_at, completed_at, row_version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, value.ID, nullableUUID(value.IntentID), nullableUUID(value.IdentityID), nullableUUID(value.ChallengeID), string(value.Status), nullableBytes(value.ResetGrantHash), nullableInt16(value.ResetGrantKeyVer), nullableInt64(value.PolicyVersion), value.ExpiresAt, nullableTime(value.ChallengeVerifiedAt), nullableTime(value.CompletedAt), value.Version, value.CreatedAt, value.UpdatedAt)
	return err
}

func (r *PostgresRepository) FindForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Reset, error) {
	if tx == nil {
		return Reset{}, errors.New("password reset transaction is required")
	}
	return scan(tx.QueryRow(ctx, resetSelect+` WHERE password_reset_id = $1 FOR UPDATE`, id))
}

func (r *PostgresRepository) Save(ctx context.Context, tx pgx.Tx, value *Reset) error {
	if tx == nil {
		return errors.New("password reset transaction is required")
	}
	if value == nil {
		return ErrInvalid
	}
	if err := value.Validate(); err != nil {
		return err
	}
	var version int64
	var updated time.Time
	err := tx.QueryRow(ctx, `
		UPDATE auth_password_resets SET identity_id=$2, challenge_id=$3, status=$4, reset_grant_hash=$5, reset_grant_key_version=$6, policy_version=$7, expires_at=$8, challenge_verified_at=$9, completed_at=$10, row_version=row_version+1, updated_at=now()
		WHERE password_reset_id=$1 AND row_version=$11 RETURNING row_version, updated_at
	`, value.ID, nullableUUID(value.IdentityID), nullableUUID(value.ChallengeID), string(value.Status), nullableBytes(value.ResetGrantHash), nullableInt16(value.ResetGrantKeyVer), nullableInt64(value.PolicyVersion), value.ExpiresAt, nullableTime(value.ChallengeVerifiedAt), nullableTime(value.CompletedAt), value.Version).Scan(&version, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVersionConflict
	}
	if err != nil {
		return err
	}
	value.Version, value.UpdatedAt = version, updated.UTC()
	return nil
}

const resetSelect = `SELECT password_reset_id, intent_id, identity_id, challenge_id, status, reset_grant_hash, reset_grant_key_version, policy_version, expires_at, challenge_verified_at, completed_at, row_version, created_at, updated_at FROM auth_password_resets`

func scan(row pgx.Row) (Reset, error) {
	var r Reset
	var intentID, identityID, challengeID pgtype.UUID
	var keyVersion pgtype.Int2
	var policyVersion pgtype.Int8
	var verified, completed pgtype.Timestamptz
	var status string
	err := row.Scan(&r.ID, &intentID, &identityID, &challengeID, &status, &r.ResetGrantHash, &keyVersion, &policyVersion, &r.ExpiresAt, &verified, &completed, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reset{}, ErrNotFound
	}
	if err != nil {
		return Reset{}, err
	}
	r.IntentID, r.IdentityID, r.ChallengeID = optionalUUID(intentID), optionalUUID(identityID), optionalUUID(challengeID)
	r.Status, r.ResetGrantKeyVer, r.PolicyVersion = Status(status), optionalInt16(keyVersion), optionalInt64(policyVersion)
	r.ChallengeVerifiedAt, r.CompletedAt = optionalTime(verified), optionalTime(completed)
	r.ExpiresAt, r.CreatedAt, r.UpdatedAt = r.ExpiresAt.UTC(), r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	r.ResetGrantHash = append([]byte(nil), r.ResetGrantHash...)
	return r, nil
}
func optionalUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}
func optionalInt16(value pgtype.Int2) *int16 {
	if !value.Valid {
		return nil
	}
	result := value.Int16
	return &result
}
func optionalInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
func nullableUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableInt16(value *int16) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}
func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}
