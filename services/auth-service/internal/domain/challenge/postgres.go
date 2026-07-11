package challenge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOptions struct {
	VirtualProjectionEnabled bool
}

// PostgresRepository keeps Challenge SQL close to the aggregate. Its methods
// receive the caller's pgx.Tx so Challenge changes can share one transaction
// with Registration, Identity, delivery outbox, and audit writes.
type PostgresRepository struct {
	pool                     *pgxpool.Pool
	virtualProjectionEnabled bool
}

func NewPostgresRepository(pool *pgxpool.Pool, options ...PostgresOptions) *PostgresRepository {
	option := PostgresOptions{}
	if len(options) > 0 {
		option = options[0]
	}
	return &PostgresRepository{pool: pool, virtualProjectionEnabled: option.VirtualProjectionEnabled}
}

func (r *PostgresRepository) Create(ctx context.Context, tx pgx.Tx, challenge Challenge) error {
	if tx == nil {
		return errors.New("challenge transaction is required")
	}
	return insert(ctx, tx, challenge)
}

func (r *PostgresRepository) Find(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID) (Challenge, error) {
	if tx == nil {
		return Challenge{}, errors.New("challenge transaction is required")
	}
	challenge, err := scanChallenge(tx.QueryRow(ctx, challengeSelect+` WHERE challenge_id = $1`, uuidParam(challengeID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, ErrNotFound
	}
	return challenge, err
}

func (r *PostgresRepository) FindForUpdate(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID) (Challenge, error) {
	if tx == nil {
		return Challenge{}, errors.New("challenge transaction is required")
	}
	challenge, err := scanChallenge(tx.QueryRow(ctx, challengeSelect+` WHERE challenge_id = $1 FOR UPDATE`, uuidParam(challengeID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, ErrNotFound
	}
	return challenge, err
}

// Issue revokes the previous issued Challenge for the same subject/purpose
// while holding those rows locked, then inserts the replacement.
func (r *PostgresRepository) Issue(ctx context.Context, tx pgx.Tx, challenge Challenge) error {
	if tx == nil {
		return errors.New("challenge transaction is required")
	}
	if err := challenge.Validate(); err != nil || challenge.Status != StatusIssued {
		if err != nil {
			return err
		}
		return ErrInvalidChallenge
	}
	// A partial unique index protects the final state, but it cannot lock a
	// missing issued row. This transaction lock also serializes two first-time
	// issuances for the same subject and purpose.
	lockKey := fmt.Sprintf("auth_challenge:%s:%s:%s", challenge.SubjectType, challenge.SubjectID, challenge.Purpose)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT challenge_id
		FROM auth_challenges
		WHERE subject_type = $1 AND subject_id = $2 AND purpose = $3 AND status = 'issued'
		FOR UPDATE
	`, string(challenge.SubjectType), uuidParam(challenge.SubjectID), string(challenge.Purpose))
	if err != nil {
		return err
	}
	defer rows.Close()
	previousIDs := make([]uuid.UUID, 0, 1)
	for rows.Next() {
		var value pgtype.UUID
		if err := rows.Scan(&value); err != nil {
			return err
		}
		id, err := requiredUUID(value)
		if err != nil {
			return err
		}
		previousIDs = append(previousIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Release the result stream before issuing updates on this transaction.
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, previousID := range previousIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_challenges
			SET status = 'revoked', closed_at = $2, row_version = row_version + 1
			WHERE challenge_id = $1 AND status = 'issued'
		`, uuidParam(previousID), now); err != nil {
			return err
		}
		if err := r.destroyVirtualProjection(ctx, tx, previousID, now); err != nil {
			return err
		}
	}
	return insert(ctx, tx, challenge)
}

// Consume locks the Challenge before verification. Business outcomes are
// returned in ConsumeResult/state; database failures are returned as errors so
// the caller can still commit failed/expired attempt updates atomically.
func (r *PostgresRepository) Consume(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID, now time.Time, verify func(Challenge) bool) (Challenge, ConsumeResult, error) {
	challenge, err := r.FindForUpdate(ctx, tx, challengeID)
	if err != nil {
		return Challenge{}, ConsumeResult{}, err
	}
	matches := false
	if verify != nil {
		matches = verify(challenge)
	}
	result, domainErr := challenge.Consume(now, matches)
	if result.Changed {
		if err := r.Save(ctx, tx, &challenge); err != nil {
			return Challenge{}, ConsumeResult{}, err
		}
	}
	if domainErr != nil {
		result.Failure = consumeFailure(domainErr)
	}
	return challenge, result, nil
}

func (r *PostgresRepository) Save(ctx context.Context, tx pgx.Tx, challenge *Challenge) error {
	if tx == nil {
		return errors.New("challenge transaction is required")
	}
	if challenge == nil {
		return ErrInvalidChallenge
	}
	if err := challenge.Validate(); err != nil {
		return err
	}
	expectedVersion := challenge.Version
	var version int64
	err := tx.QueryRow(ctx, `
		UPDATE auth_challenges
		SET
			status = $2,
			attempt_count = $3,
			send_count = $4,
			next_send_at = $5,
			expires_at = $6,
			consumed_at = $7,
			verified_at = $8,
			closed_at = $9,
			row_version = row_version + 1
		WHERE challenge_id = $1 AND row_version = $10
		RETURNING row_version
	`,
		uuidParam(challenge.ID),
		string(challenge.Status),
		challenge.AttemptCount,
		challenge.SendCount,
		challenge.NextSendAt,
		challenge.ExpiresAt,
		nullableTimeParam(challenge.ConsumedAt),
		nullableTimeParam(challenge.VerifiedAt),
		nullableTimeParam(challenge.ClosedAt),
		expectedVersion,
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVersionConflict
	}
	if err != nil {
		return err
	}
	challenge.Version = version
	if challenge.IsTerminal() {
		if err := r.destroyVirtualProjection(ctx, tx, challenge.ID, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) StoreDeliveryPayload(ctx context.Context, tx pgx.Tx, payload DeliveryPayload) error {
	if tx == nil {
		return errors.New("challenge transaction is required")
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_verification_delivery_payloads (
			delivery_payload_id, challenge_id, send_sequence, payload_ciphertext,
			payload_key_id, aad_hash, delivery_status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, now())
	`, payload.ID, payload.ChallengeID, payload.SendSequence, payload.Ciphertext, payload.KeyID, payload.AADHash, payload.ExpiresAt)
	return err
}

func (r *PostgresRepository) StoreVirtualProjection(ctx context.Context, tx pgx.Tx, projection VirtualProjection) error {
	if !r.virtualProjectionEnabled {
		return ErrVirtualProjectionDisabled
	}
	if tx == nil {
		return errors.New("challenge transaction is required")
	}
	if err := projection.Validate(); err != nil {
		return err
	}
	var status string
	var version int64
	err := tx.QueryRow(ctx, `
		SELECT status, row_version
		FROM auth_challenges
		WHERE challenge_id = $1
		FOR UPDATE
	`, uuidParam(projection.ChallengeID)).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if Status(status) != StatusIssued || version != projection.ChallengeVersion {
		return ErrVirtualUnavailable
	}
	_, err = tx.Exec(ctx, `
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
	`,
		uuidParam(projection.ChallengeID),
		string(projection.Channel),
		projection.ChallengeVersion,
		nullableBytes(projection.CodeCiphertext),
		nullableString(projection.CodeKeyID),
		projection.MaskedDestination,
		string(projection.Status),
		projection.ExpiresAt,
		nullableTimeParam(projection.DestroyedAt),
		projection.CreatedAt,
	)
	return err
}

func (r *PostgresRepository) FindVirtualProjection(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID, now time.Time) (VirtualProjection, error) {
	if !r.virtualProjectionEnabled {
		return VirtualProjection{}, ErrVirtualProjectionDisabled
	}
	if tx == nil {
		return VirtualProjection{}, errors.New("challenge transaction is required")
	}
	var (
		projection                                 VirtualProjection
		challengeIDValue                           pgtype.UUID
		channel, projectionStatus, challengeStatus string
		challengeVersion, currentChallengeVersion  int64
		destroyedAt                                pgtype.Timestamptz
		codeKeyID                                  pgtype.Text
	)
	err := tx.QueryRow(ctx, `
		SELECT
			p.challenge_id, p.channel, p.challenge_version, p.code_ciphertext,
			p.code_key_id, p.masked_destination, p.status, p.expires_at,
			p.destroyed_at, p.created_at, c.status, c.row_version
		FROM auth_virtual_verification_messages p
		JOIN auth_challenges c ON c.challenge_id = p.challenge_id
		WHERE p.challenge_id = $1
		FOR UPDATE OF p, c
	`, uuidParam(challengeID)).Scan(
		&challengeIDValue, &channel, &challengeVersion, &projection.CodeCiphertext,
		&codeKeyID, &projection.MaskedDestination, &projectionStatus, &projection.ExpiresAt,
		&destroyedAt, &projection.CreatedAt, &challengeStatus, &currentChallengeVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return VirtualProjection{}, ErrVirtualUnavailable
	}
	if err != nil {
		return VirtualProjection{}, err
	}
	projection.ChallengeID, err = requiredUUID(challengeIDValue)
	if err != nil {
		return VirtualProjection{}, err
	}
	projection.Channel = Channel(channel)
	projection.ChallengeVersion = challengeVersion
	projection.CodeKeyID = optionalString(codeKeyID)
	projection.Status = VirtualStatus(projectionStatus)
	projection.DestroyedAt = optionalTime(destroyedAt)
	projection.ExpiresAt = projection.ExpiresAt.UTC()
	projection.CreatedAt = projection.CreatedAt.UTC()
	projection.CodeCiphertext = copyBytes(projection.CodeCiphertext)
	if Status(challengeStatus) != StatusIssued || currentChallengeVersion != projection.ChallengeVersion || projection.Status != VirtualReady || !now.UTC().Before(projection.ExpiresAt) {
		return VirtualProjection{}, ErrVirtualUnavailable
	}
	return projection, nil
}

func (r *PostgresRepository) destroyVirtualProjection(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID, now time.Time) error {
	if !r.virtualProjectionEnabled {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE auth_virtual_verification_messages
		SET status = 'destroyed', code_ciphertext = NULL, code_key_id = NULL,
			destroyed_at = COALESCE(destroyed_at, $2)
		WHERE challenge_id = $1 AND status <> 'destroyed'
	`, uuidParam(challengeID), now.UTC())
	return err
}

func insert(ctx context.Context, queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, challenge Challenge) error {
	if err := challenge.Validate(); err != nil {
		return err
	}
	_, err := queryer.Exec(ctx, `
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
	`,
		uuidParam(challenge.ID),
		string(challenge.SubjectType),
		uuidParam(challenge.SubjectID),
		string(challenge.Purpose),
		string(challenge.Method),
		string(challenge.Channel),
		challenge.Destination,
		nullableBytes(challenge.DestinationLookupHash),
		nullableUUIDParam(challenge.IdentityID),
		challenge.CodeHash,
		challenge.VerifierKeyVersion,
		string(challenge.Status),
		challenge.AttemptCount,
		challenge.MaxAttempts,
		challenge.SendCount,
		challenge.MaxSends,
		challenge.NextSendAt,
		nullableInt64Param(challenge.PolicyVersion),
		challenge.ExpiresAt,
		nullableTimeParam(challenge.ConsumedAt),
		nullableTimeParam(challenge.VerifiedAt),
		nullableTimeParam(challenge.ClosedAt),
		challenge.Version,
		challenge.CreatedAt,
	)
	return err
}

const challengeSelect = `
	SELECT challenge_id, subject_type, subject_id, purpose, method, channel,
		destination, destination_lookup_hash, identity_id, code_hash,
		verifier_key_version, status, attempt_count, max_attempts, send_count,
		max_sends, next_send_at, policy_version, expires_at, consumed_at,
		verified_at, closed_at, row_version, created_at
	FROM auth_challenges`

func scanChallenge(row pgx.Row) (Challenge, error) {
	var (
		id, subjectID, identityID             pgtype.UUID
		policyVersion                         pgtype.Int8
		consumedAt, verifiedAt, closedAt      pgtype.Timestamptz
		subjectType, purpose, method, channel string
		status                                string
		challenge                             Challenge
	)
	err := row.Scan(
		&id, &subjectType, &subjectID, &purpose, &method, &channel,
		&challenge.Destination, &challenge.DestinationLookupHash, &identityID, &challenge.CodeHash,
		&challenge.VerifierKeyVersion, &status, &challenge.AttemptCount, &challenge.MaxAttempts,
		&challenge.SendCount, &challenge.MaxSends, &challenge.NextSendAt, &policyVersion,
		&challenge.ExpiresAt, &consumedAt, &verifiedAt, &closedAt, &challenge.Version, &challenge.CreatedAt,
	)
	if err != nil {
		return Challenge{}, err
	}
	var conversionErr error
	if challenge.ID, conversionErr = requiredUUID(id); conversionErr != nil {
		return Challenge{}, conversionErr
	}
	if challenge.SubjectID, conversionErr = requiredUUID(subjectID); conversionErr != nil {
		return Challenge{}, conversionErr
	}
	challenge.SubjectType = SubjectType(subjectType)
	challenge.Purpose = Purpose(purpose)
	challenge.Method = Method(method)
	challenge.Channel = Channel(channel)
	challenge.Status = Status(status)
	challenge.IdentityID = optionalUUID(identityID)
	challenge.PolicyVersion = optionalInt64(policyVersion)
	challenge.ConsumedAt = optionalTime(consumedAt)
	challenge.VerifiedAt = optionalTime(verifiedAt)
	challenge.ClosedAt = optionalTime(closedAt)
	challenge.DestinationLookupHash = copyBytes(challenge.DestinationLookupHash)
	challenge.CodeHash = copyBytes(challenge.CodeHash)
	challenge.NextSendAt = challenge.NextSendAt.UTC()
	challenge.ExpiresAt = challenge.ExpiresAt.UTC()
	challenge.CreatedAt = challenge.CreatedAt.UTC()
	return challenge, nil
}

func consumeFailure(err error) ConsumeFailure {
	switch {
	case errors.Is(err, ErrChallengeExpired):
		return ConsumeFailureExpired
	case errors.Is(err, ErrVerificationFailed):
		return ConsumeFailureMismatch
	case errors.Is(err, ErrChallengeClosed):
		return ConsumeFailureClosed
	default:
		return ConsumeFailureInvalid
	}
}

func requiredUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, fmt.Errorf("%w: required UUID is null", ErrInvalidChallenge)
	}
	return uuid.UUID(value.Bytes), nil
}

func optionalUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := uuid.UUID(value.Bytes)
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

func optionalString(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func uuidParam(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func nullableUUIDParam(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return uuidParam(*value)
}

func nullableInt64Param(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTimeParam(value *time.Time) any {
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

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
