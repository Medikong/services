package postgres

import (
	"context"
	"errors"
	"time"

	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PasswordResetRepository struct {
	tx pgx.Tx
}

func NewPasswordResetRepository(tx pgx.Tx) *PasswordResetRepository {
	return &PasswordResetRepository{tx: tx}
}

func (r *PasswordResetRepository) Create(ctx context.Context, value domainpasswordreset.Reset) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_password_resets (
			password_reset_id, intent_id, identity_id, challenge_id, status,
			reset_grant_hash, reset_grant_key_version, policy_version, expires_at,
			challenge_verified_at, completed_at, row_version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, value.ID, passwordResetNullableUUID(value.IntentID), passwordResetNullableUUID(value.IdentityID),
		passwordResetNullableUUID(value.ChallengeID), string(value.Status), passwordResetNullableBytes(value.ResetGrantHash),
		passwordResetNullableInt16(value.ResetGrantKeyVer), passwordResetNullableInt64(value.PolicyVersion), value.ExpiresAt,
		passwordResetNullableTime(value.ChallengeVerifiedAt), passwordResetNullableTime(value.CompletedAt),
		value.Version, value.CreatedAt, value.UpdatedAt)
	return err
}

func (r *PasswordResetRepository) FindForUpdate(ctx context.Context, id uuid.UUID) (domainpasswordreset.Reset, error) {
	return scanPasswordReset(r.tx.QueryRow(ctx, passwordResetSelect+` WHERE password_reset_id = $1 FOR UPDATE`, id))
}

func (r *PasswordResetRepository) Save(ctx context.Context, value *domainpasswordreset.Reset) error {
	if value == nil {
		return domainpasswordreset.ErrInvalid
	}
	if err := value.Validate(); err != nil {
		return err
	}
	var version int64
	var updatedAt time.Time
	err := r.tx.QueryRow(ctx, `
		UPDATE auth_password_resets
		SET identity_id = $2, challenge_id = $3, status = $4, reset_grant_hash = $5,
			reset_grant_key_version = $6, policy_version = $7, expires_at = $8,
			challenge_verified_at = $9, completed_at = $10,
			row_version = row_version + 1, updated_at = now()
		WHERE password_reset_id = $1 AND row_version = $11
		RETURNING row_version, updated_at
	`, value.ID, passwordResetNullableUUID(value.IdentityID), passwordResetNullableUUID(value.ChallengeID),
		string(value.Status), passwordResetNullableBytes(value.ResetGrantHash), passwordResetNullableInt16(value.ResetGrantKeyVer),
		passwordResetNullableInt64(value.PolicyVersion), value.ExpiresAt, passwordResetNullableTime(value.ChallengeVerifiedAt),
		passwordResetNullableTime(value.CompletedAt), value.Version).Scan(&version, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainpasswordreset.ErrVersionConflict
	}
	if err != nil {
		return err
	}
	value.Version = version
	value.UpdatedAt = updatedAt.UTC()
	return nil
}

const passwordResetSelect = `
	SELECT password_reset_id, intent_id, identity_id, challenge_id, status,
		reset_grant_hash, reset_grant_key_version, policy_version, expires_at,
		challenge_verified_at, completed_at, row_version, created_at, updated_at
	FROM auth_password_resets`

func scanPasswordReset(row pgx.Row) (domainpasswordreset.Reset, error) {
	var value domainpasswordreset.Reset
	var intentID, identityID, challengeID pgtype.UUID
	var grantKeyVersion pgtype.Int2
	var policyVersion pgtype.Int8
	var challengeVerifiedAt, completedAt pgtype.Timestamptz
	var status string
	err := row.Scan(
		&value.ID, &intentID, &identityID, &challengeID, &status,
		&value.ResetGrantHash, &grantKeyVersion, &policyVersion, &value.ExpiresAt,
		&challengeVerifiedAt, &completedAt, &value.Version, &value.CreatedAt, &value.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainpasswordreset.Reset{}, domainpasswordreset.ErrNotFound
	}
	if err != nil {
		return domainpasswordreset.Reset{}, err
	}
	value.IntentID = passwordResetOptionalUUID(intentID)
	value.IdentityID = passwordResetOptionalUUID(identityID)
	value.ChallengeID = passwordResetOptionalUUID(challengeID)
	value.Status = domainpasswordreset.Status(status)
	value.ResetGrantKeyVer = passwordResetOptionalInt16(grantKeyVersion)
	value.PolicyVersion = passwordResetOptionalInt64(policyVersion)
	value.ChallengeVerifiedAt = passwordResetOptionalTime(challengeVerifiedAt)
	value.CompletedAt = passwordResetOptionalTime(completedAt)
	value.ExpiresAt = value.ExpiresAt.UTC()
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.UpdatedAt.UTC()
	value.ResetGrantHash = append([]byte(nil), value.ResetGrantHash...)
	return value, nil
}

func passwordResetOptionalUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := uuid.UUID(value.Bytes)
	return &result
}

func passwordResetOptionalInt16(value pgtype.Int2) *int16 {
	if !value.Valid {
		return nil
	}
	result := value.Int16
	return &result
}

func passwordResetOptionalInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func passwordResetOptionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func passwordResetNullableUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}

func passwordResetNullableInt16(value *int16) any {
	if value == nil {
		return nil
	}
	return *value
}

func passwordResetNullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func passwordResetNullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func passwordResetNullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}
