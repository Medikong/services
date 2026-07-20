package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/samber/oops"
)

type ChallengeOptions struct {
	VirtualProjectionEnabled bool
}

// ChallengeRepository is bound to one caller-owned PostgreSQL transaction.
// The application composes this with the other repositories that participate
// in the same use-case transaction.
type ChallengeRepository struct {
	tx                       pgx.Tx
	virtualProjectionEnabled bool
}

func NewChallengeRepository(tx pgx.Tx, options ...ChallengeOptions) *ChallengeRepository {
	option := ChallengeOptions{}
	if len(options) > 0 {
		option = options[0]
	}
	return &ChallengeRepository{tx: tx, virtualProjectionEnabled: option.VirtualProjectionEnabled}
}

func (r *ChallengeRepository) Create(ctx context.Context, value domainchallenge.Challenge) error {
	if err := r.requireTransaction(); err != nil {
		return err
	}
	return r.insert(ctx, value)
}

func (r *ChallengeRepository) Find(ctx context.Context, challengeID uuid.UUID) (domainchallenge.Challenge, error) {
	if err := r.requireTransaction(); err != nil {
		return domainchallenge.Challenge{}, err
	}
	value, err := scanChallenge(r.tx.QueryRow(ctx, challengeSelect+` WHERE challenge_id = $1`, challengeUUIDParam(challengeID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domainchallenge.Challenge{}, domainchallenge.ErrNotFound
	}
	return value, err
}

func (r *ChallengeRepository) FindForUpdate(ctx context.Context, challengeID uuid.UUID) (domainchallenge.Challenge, error) {
	if err := r.requireTransaction(); err != nil {
		return domainchallenge.Challenge{}, err
	}
	value, err := scanChallenge(r.tx.QueryRow(ctx, challengeSelect+` WHERE challenge_id = $1 FOR UPDATE`, challengeUUIDParam(challengeID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domainchallenge.Challenge{}, domainchallenge.ErrNotFound
	}
	return value, err
}

// Issue serializes first issue and reissue for a subject/purpose, destroys the
// previous development projection, then inserts the replacement atomically.
func (r *ChallengeRepository) Issue(ctx context.Context, value domainchallenge.Challenge) error {
	if err := r.requireTransaction(); err != nil {
		return err
	}
	if err := value.Validate(); err != nil || value.Status != domainchallenge.StatusIssued {
		if err != nil {
			return err
		}
		return domainchallenge.ErrInvalidChallenge
	}
	lockKey := fmt.Sprintf("auth_challenge:%s:%s:%s", value.SubjectType, value.SubjectID, value.Purpose)
	if _, err := r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return err
	}
	rows, err := r.tx.Query(ctx, `
		SELECT challenge_id
		FROM auth_challenges
		WHERE subject_type = $1 AND subject_id = $2 AND purpose = $3 AND status = 'issued'
		FOR UPDATE
	`, string(value.SubjectType), challengeUUIDParam(value.SubjectID), string(value.Purpose))
	if err != nil {
		return err
	}
	defer rows.Close()
	previousIDs := make([]uuid.UUID, 0, 1)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		previousID, err := requiredChallengeUUID(id)
		if err != nil {
			return err
		}
		previousIDs = append(previousIDs, previousID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, previousID := range previousIDs {
		if _, err := r.tx.Exec(ctx, `
			UPDATE auth_challenges
			SET status = 'revoked', closed_at = $2, row_version = row_version + 1
			WHERE challenge_id = $1 AND status = 'issued'
		`, challengeUUIDParam(previousID), now); err != nil {
			return err
		}
		if err := r.destroyVirtualProjection(ctx, previousID, now); err != nil {
			return err
		}
	}
	return r.insert(ctx, value)
}

func (r *ChallengeRepository) Save(ctx context.Context, value *domainchallenge.Challenge) error {
	if err := r.requireTransaction(); err != nil {
		return err
	}
	if value == nil {
		return domainchallenge.ErrInvalidChallenge
	}
	if err := value.Validate(); err != nil {
		return err
	}
	expectedVersion := value.Version
	var version int64
	err := r.tx.QueryRow(ctx, `
		UPDATE auth_challenges
		SET status = $2, attempt_count = $3, send_count = $4, next_send_at = $5,
			expires_at = $6, consumed_at = $7, verified_at = $8, closed_at = $9,
			row_version = row_version + 1
		WHERE challenge_id = $1 AND row_version = $10
		RETURNING row_version
	`,
		challengeUUIDParam(value.ID), string(value.Status), value.AttemptCount, value.SendCount,
		value.NextSendAt, value.ExpiresAt, nullableChallengeTimeParam(value.ConsumedAt),
		nullableChallengeTimeParam(value.VerifiedAt), nullableChallengeTimeParam(value.ClosedAt), expectedVersion,
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainchallenge.ErrVersionConflict
	}
	if err != nil {
		return err
	}
	value.Version = version
	if value.IsTerminal() {
		return r.destroyVirtualProjection(ctx, value.ID, time.Now().UTC())
	}
	return nil
}

func (r *ChallengeRepository) StoreDeliveryPayload(ctx context.Context, payload domainchallenge.DeliveryPayload) error {
	if err := r.requireTransaction(); err != nil {
		return err
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_verification_delivery_payloads (
			delivery_payload_id, challenge_id, send_sequence, payload_ciphertext,
			payload_key_id, aad_hash, delivery_status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, now())
	`, payload.ID, payload.ChallengeID, payload.SendSequence, payload.Ciphertext, payload.KeyID, payload.AADHash, payload.ExpiresAt)
	return err
}

func (r *ChallengeRepository) StoreVirtualProjection(ctx context.Context, projection domainchallenge.VirtualProjection) error {
	if !r.virtualProjectionEnabled {
		return domainchallenge.ErrVirtualProjectionDisabled
	}
	if err := r.requireTransaction(); err != nil {
		return err
	}
	if err := projection.Validate(); err != nil {
		return err
	}
	var status string
	var version int64
	err := r.tx.QueryRow(ctx, `
		SELECT status, row_version FROM auth_challenges
		WHERE challenge_id = $1 FOR UPDATE
	`, challengeUUIDParam(projection.ChallengeID)).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainchallenge.ErrNotFound
	}
	if err != nil {
		return err
	}
	if domainchallenge.Status(status) != domainchallenge.StatusIssued || version != projection.ChallengeVersion {
		return domainchallenge.ErrVirtualUnavailable
	}
	_, err = r.tx.Exec(ctx, `
		INSERT INTO auth_virtual_verification_messages (
			challenge_id, channel, challenge_version, code_ciphertext, code_key_id,
			masked_destination, status, expires_at, destroyed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (challenge_id) DO UPDATE
		SET channel = EXCLUDED.channel,
			challenge_version = EXCLUDED.challenge_version,
			code_ciphertext = EXCLUDED.code_ciphertext,
			code_key_id = EXCLUDED.code_key_id,
			masked_destination = EXCLUDED.masked_destination,
			status = EXCLUDED.status,
			expires_at = EXCLUDED.expires_at,
			destroyed_at = EXCLUDED.destroyed_at
	`, challengeUUIDParam(projection.ChallengeID), string(projection.Channel), projection.ChallengeVersion,
		nullableChallengeBytes(projection.CodeCiphertext), nullableChallengeString(projection.CodeKeyID),
		projection.MaskedDestination, string(projection.Status), projection.ExpiresAt,
		nullableChallengeTimeParam(projection.DestroyedAt), projection.CreatedAt)
	return err
}

func (r *ChallengeRepository) FindVirtualProjection(ctx context.Context, challengeID uuid.UUID, now time.Time) (domainchallenge.VirtualProjection, error) {
	if !r.virtualProjectionEnabled {
		return domainchallenge.VirtualProjection{}, domainchallenge.ErrVirtualProjectionDisabled
	}
	if err := r.requireTransaction(); err != nil {
		return domainchallenge.VirtualProjection{}, err
	}
	var (
		projection                                 domainchallenge.VirtualProjection
		challengeIDValue                           pgtype.UUID
		channel, projectionStatus, challengeStatus string
		challengeVersion, currentChallengeVersion  int64
		destroyedAt                                pgtype.Timestamptz
		codeKeyID                                  pgtype.Text
	)
	err := r.tx.QueryRow(ctx, `
		SELECT p.challenge_id, p.channel, p.challenge_version, p.code_ciphertext,
			p.code_key_id, p.masked_destination, p.status, p.expires_at,
			p.destroyed_at, p.created_at, c.status, c.row_version
		FROM auth_virtual_verification_messages p
		JOIN auth_challenges c ON c.challenge_id = p.challenge_id
		WHERE p.challenge_id = $1
		FOR UPDATE OF p, c
	`, challengeUUIDParam(challengeID)).Scan(
		&challengeIDValue, &channel, &challengeVersion, &projection.CodeCiphertext,
		&codeKeyID, &projection.MaskedDestination, &projectionStatus, &projection.ExpiresAt,
		&destroyedAt, &projection.CreatedAt, &challengeStatus, &currentChallengeVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainchallenge.VirtualProjection{}, domainchallenge.ErrVirtualUnavailable
	}
	if err != nil {
		return domainchallenge.VirtualProjection{}, err
	}
	projection.ChallengeID, err = requiredChallengeUUID(challengeIDValue)
	if err != nil {
		return domainchallenge.VirtualProjection{}, err
	}
	projection.Channel = domainchallenge.Channel(channel)
	projection.ChallengeVersion = challengeVersion
	projection.CodeKeyID = optionalChallengeString(codeKeyID)
	projection.Status = domainchallenge.VirtualStatus(projectionStatus)
	projection.DestroyedAt = optionalChallengeTime(destroyedAt)
	projection.ExpiresAt = projection.ExpiresAt.UTC()
	projection.CreatedAt = projection.CreatedAt.UTC()
	projection.CodeCiphertext = append([]byte(nil), projection.CodeCiphertext...)
	if domainchallenge.Status(challengeStatus) != domainchallenge.StatusIssued ||
		currentChallengeVersion != projection.ChallengeVersion ||
		projection.Status != domainchallenge.VirtualReady ||
		!now.UTC().Before(projection.ExpiresAt) {
		return domainchallenge.VirtualProjection{}, domainchallenge.ErrVirtualUnavailable
	}
	return projection, nil
}

func (r *ChallengeRepository) destroyVirtualProjection(ctx context.Context, challengeID uuid.UUID, now time.Time) error {
	if !r.virtualProjectionEnabled {
		return nil
	}
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_virtual_verification_messages
		SET status = 'destroyed', code_ciphertext = NULL, code_key_id = NULL,
			destroyed_at = COALESCE(destroyed_at, $2)
		WHERE challenge_id = $1 AND status <> 'destroyed'
	`, challengeUUIDParam(challengeID), now.UTC())
	return err
}

func (r *ChallengeRepository) insert(ctx context.Context, value domainchallenge.Challenge) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_challenges (
			challenge_id, subject_type, subject_id, purpose, method, channel,
			destination, destination_lookup_hash, identity_id, code_hash,
			verifier_key_version, status, attempt_count, max_attempts, send_count,
			max_sends, next_send_at, policy_version, expires_at, consumed_at,
			verified_at, closed_at, row_version, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24
		)
	`, challengeUUIDParam(value.ID), string(value.SubjectType), challengeUUIDParam(value.SubjectID),
		string(value.Purpose), string(value.Method), string(value.Channel), value.Destination,
		nullableChallengeBytes(value.DestinationLookupHash), nullableChallengeUUIDParam(value.IdentityID), value.CodeHash,
		value.VerifierKeyVersion, string(value.Status), value.AttemptCount, value.MaxAttempts,
		value.SendCount, value.MaxSends, value.NextSendAt, nullableChallengeInt64Param(value.PolicyVersion),
		value.ExpiresAt, nullableChallengeTimeParam(value.ConsumedAt), nullableChallengeTimeParam(value.VerifiedAt),
		nullableChallengeTimeParam(value.ClosedAt), value.Version, value.CreatedAt)
	return err
}

func (r *ChallengeRepository) requireTransaction() error {
	if r != nil && r.tx != nil {
		return nil
	}
	return oops.In("challenge_repository").Code("challenge.transaction_required").New("challenge transaction is required")
}

const challengeSelect = `
	SELECT challenge_id, subject_type, subject_id, purpose, method, channel,
		destination, destination_lookup_hash, identity_id, code_hash,
		verifier_key_version, status, attempt_count, max_attempts, send_count,
		max_sends, next_send_at, policy_version, expires_at, consumed_at,
		verified_at, closed_at, row_version, created_at
	FROM auth_challenges`

func scanChallenge(row pgx.Row) (domainchallenge.Challenge, error) {
	var (
		id, subjectID, identityID             pgtype.UUID
		policyVersion                         pgtype.Int8
		consumedAt, verifiedAt, closedAt      pgtype.Timestamptz
		subjectType, purpose, method, channel string
		status                                string
		value                                 domainchallenge.Challenge
	)
	err := row.Scan(
		&id, &subjectType, &subjectID, &purpose, &method, &channel,
		&value.Destination, &value.DestinationLookupHash, &identityID, &value.CodeHash,
		&value.VerifierKeyVersion, &status, &value.AttemptCount, &value.MaxAttempts,
		&value.SendCount, &value.MaxSends, &value.NextSendAt, &policyVersion,
		&value.ExpiresAt, &consumedAt, &verifiedAt, &closedAt, &value.Version, &value.CreatedAt,
	)
	if err != nil {
		return domainchallenge.Challenge{}, err
	}
	var conversionErr error
	if value.ID, conversionErr = requiredChallengeUUID(id); conversionErr != nil {
		return domainchallenge.Challenge{}, conversionErr
	}
	if value.SubjectID, conversionErr = requiredChallengeUUID(subjectID); conversionErr != nil {
		return domainchallenge.Challenge{}, conversionErr
	}
	value.SubjectType = domainchallenge.SubjectType(subjectType)
	value.Purpose = domainchallenge.Purpose(purpose)
	value.Method = domainchallenge.Method(method)
	value.Channel = domainchallenge.Channel(channel)
	value.Status = domainchallenge.Status(status)
	value.IdentityID = optionalChallengeUUID(identityID)
	value.PolicyVersion = optionalChallengeInt64(policyVersion)
	value.ConsumedAt = optionalChallengeTime(consumedAt)
	value.VerifiedAt = optionalChallengeTime(verifiedAt)
	value.ClosedAt = optionalChallengeTime(closedAt)
	value.DestinationLookupHash = append([]byte(nil), value.DestinationLookupHash...)
	value.CodeHash = append([]byte(nil), value.CodeHash...)
	value.NextSendAt = value.NextSendAt.UTC()
	value.ExpiresAt = value.ExpiresAt.UTC()
	value.CreatedAt = value.CreatedAt.UTC()
	return value, nil
}

func requiredChallengeUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, oops.In("challenge_repository").Code("challenge.required_uuid_null").Wrap(domainchallenge.ErrInvalidChallenge)
	}
	return uuid.UUID(value.Bytes), nil
}

func optionalChallengeUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := uuid.UUID(value.Bytes)
	return &result
}

func optionalChallengeInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func optionalChallengeTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func optionalChallengeString(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func challengeUUIDParam(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func nullableChallengeUUIDParam(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return challengeUUIDParam(*value)
}

func nullableChallengeInt64Param(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableChallengeTimeParam(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func nullableChallengeBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func nullableChallengeString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var _ domainchallenge.Repository = (*ChallengeRepository)(nil)
