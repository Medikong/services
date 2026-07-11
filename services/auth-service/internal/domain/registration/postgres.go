package registration

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

// PostgresRepository keeps Registration SQL beside the domain. It deliberately
// uses pgxpool and pgx.Tx directly rather than a cross-domain store layer.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, tx pgx.Tx, registration Registration) error {
	if tx == nil {
		return errors.New("registration transaction is required")
	}
	return insert(ctx, tx, registration)
}

func (r *PostgresRepository) Find(ctx context.Context, tx pgx.Tx, registrationID uuid.UUID) (Registration, error) {
	if tx == nil {
		return Registration{}, errors.New("registration transaction is required")
	}
	registration, err := scanRegistration(tx.QueryRow(ctx, registrationSelect+` WHERE registration_id = $1`, uuidParam(registrationID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Registration{}, ErrNotFound
	}
	return registration, err
}

func (r *PostgresRepository) FindForUpdate(ctx context.Context, tx pgx.Tx, registrationID uuid.UUID) (Registration, error) {
	if tx == nil {
		return Registration{}, errors.New("registration transaction is required")
	}
	registration, err := scanRegistration(tx.QueryRow(ctx, registrationSelect+` WHERE registration_id = $1 FOR UPDATE`, uuidParam(registrationID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Registration{}, ErrNotFound
	}
	return registration, err
}

func (r *PostgresRepository) Save(ctx context.Context, tx pgx.Tx, registration *Registration) error {
	if tx == nil {
		return errors.New("registration transaction is required")
	}
	if registration == nil {
		return ErrInvalidRegistration
	}
	if err := registration.Validate(); err != nil {
		return err
	}
	expectedVersion := registration.Version
	var version int64
	var updatedAt time.Time
	err := tx.QueryRow(ctx, `
		UPDATE auth_registrations
		SET
			email_challenge_id = $2,
			phone_challenge_id = $3,
			remember_me = $4,
			client_channel = $5,
			status = $6,
			verified_methods = $7,
			status_token_hash = $8,
			status_token_key_version = $9,
			status_token_expires_at = $10,
			verification_binding_id = $11,
			verification_registration_version = $12,
			verification_snapshot_hash = $13,
			verification_completed_event_id = $14,
			link_request_id = $15,
			completion_idempotency_record_id = $16,
			user_id = $17,
			linked_at = $18,
			session_id = $19,
			session_policy_version = $20,
			link_accept_until = $21,
			session_issue_until = $22,
			failure_code = $23,
			expires_at = $24,
			completed_at = $25,
			row_version = row_version + 1,
			updated_at = now()
		WHERE registration_id = $1 AND row_version = $26
		RETURNING row_version, updated_at
	`,
		uuidParam(registration.ID),
		nullableUUIDParam(registration.EmailChallengeID),
		nullableUUIDParam(registration.PhoneChallengeID),
		registration.RememberMe,
		registration.ClientChannel,
		string(registration.Status),
		methodsParam(registration.VerifiedMethods),
		registration.StatusTokenHash,
		registration.StatusTokenKeyVer,
		registration.StatusTokenExpires,
		nullableUUIDParam(registration.VerificationBinding),
		nullableInt64Param(registration.VerificationVersion),
		nullableBytes(registration.VerificationHash),
		nullableUUIDParam(registration.VerificationEventID),
		nullableUUIDParam(registration.LinkRequestID),
		nullableUUIDParam(registration.CompletionIdempotencyRecordID),
		nullableUUIDParam(registration.UserID),
		nullableTimeParam(registration.LinkedAt),
		nullableUUIDParam(registration.SessionID),
		nullableInt64Param(registration.SessionPolicyVersion),
		nullableTimeParam(registration.LinkAcceptUntil),
		nullableTimeParam(registration.SessionIssueUntil),
		nullableStringParam(registration.FailureCode),
		registration.ExpiresAt,
		nullableTimeParam(registration.CompletedAt),
		expectedVersion,
	).Scan(&version, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVersionConflict
	}
	if err != nil {
		return err
	}
	registration.Version = version
	registration.UpdatedAt = updatedAt.UTC()
	return nil
}

func insert(ctx context.Context, queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, registration Registration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	_, err := queryer.Exec(ctx, `
		INSERT INTO auth_registrations (
			registration_id, intent_id, email_identity_id, phone_identity_id,
			email_challenge_id, phone_challenge_id, profile_request_id, agreement_receipt_id,
			remember_me, client_channel, status, verified_methods, status_token_hash,
			status_token_key_version, status_token_expires_at, verification_binding_id,
			verification_registration_version, verification_snapshot_hash,
			verification_completed_event_id, link_request_id, completion_idempotency_record_id,
			user_id, linked_at, session_id, session_policy_version, link_accept_until,
			session_issue_until, failure_code, expires_at, completed_at, row_version,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28,
			$29, $30, $31, $32, $33
		)
	`,
		uuidParam(registration.ID),
		uuidParam(registration.IntentID),
		uuidParam(registration.EmailIdentityID),
		uuidParam(registration.PhoneIdentityID),
		nullableUUIDParam(registration.EmailChallengeID),
		nullableUUIDParam(registration.PhoneChallengeID),
		registration.ProfileRequestID,
		registration.AgreementReceiptID,
		registration.RememberMe,
		registration.ClientChannel,
		string(registration.Status),
		methodsParam(registration.VerifiedMethods),
		registration.StatusTokenHash,
		registration.StatusTokenKeyVer,
		registration.StatusTokenExpires,
		nullableUUIDParam(registration.VerificationBinding),
		nullableInt64Param(registration.VerificationVersion),
		nullableBytes(registration.VerificationHash),
		nullableUUIDParam(registration.VerificationEventID),
		nullableUUIDParam(registration.LinkRequestID),
		nullableUUIDParam(registration.CompletionIdempotencyRecordID),
		nullableUUIDParam(registration.UserID),
		nullableTimeParam(registration.LinkedAt),
		nullableUUIDParam(registration.SessionID),
		nullableInt64Param(registration.SessionPolicyVersion),
		nullableTimeParam(registration.LinkAcceptUntil),
		nullableTimeParam(registration.SessionIssueUntil),
		nullableStringParam(registration.FailureCode),
		registration.ExpiresAt,
		nullableTimeParam(registration.CompletedAt),
		registration.Version,
		registration.CreatedAt,
		registration.UpdatedAt,
	)
	return err
}

const registrationSelect = `
	SELECT registration_id, intent_id, email_identity_id, phone_identity_id,
		email_challenge_id, phone_challenge_id, profile_request_id, agreement_receipt_id,
		remember_me, client_channel, status, verified_methods, status_token_hash,
		status_token_key_version, status_token_expires_at, verification_binding_id,
		verification_registration_version, verification_snapshot_hash,
		verification_completed_event_id, link_request_id, completion_idempotency_record_id,
		user_id, linked_at, session_id, session_policy_version, link_accept_until,
		session_issue_until, failure_code, expires_at, completed_at, row_version,
		created_at, updated_at
	FROM auth_registrations`

func scanRegistration(row pgx.Row) (Registration, error) {
	var (
		id, intentID, emailIdentityID, phoneIdentityID                               pgtype.UUID
		emailChallengeID, phoneChallengeID, verificationBinding, verificationEventID pgtype.UUID
		linkRequestID, completionID, userID, sessionID                               pgtype.UUID
		verificationVersion, sessionPolicyVersion                                    pgtype.Int8
		linkedAt, linkAcceptUntil, sessionIssueUntil, completedAt                    pgtype.Timestamptz
		failureCode                                                                  pgtype.Text
		status                                                                       string
		methods                                                                      []string
		registration                                                                 Registration
	)
	err := row.Scan(
		&id, &intentID, &emailIdentityID, &phoneIdentityID,
		&emailChallengeID, &phoneChallengeID, &registration.ProfileRequestID, &registration.AgreementReceiptID,
		&registration.RememberMe, &registration.ClientChannel, &status, &methods, &registration.StatusTokenHash,
		&registration.StatusTokenKeyVer, &registration.StatusTokenExpires, &verificationBinding,
		&verificationVersion, &registration.VerificationHash, &verificationEventID,
		&linkRequestID, &completionID, &userID, &linkedAt, &sessionID, &sessionPolicyVersion,
		&linkAcceptUntil, &sessionIssueUntil, &failureCode, &registration.ExpiresAt, &completedAt,
		&registration.Version, &registration.CreatedAt, &registration.UpdatedAt,
	)
	if err != nil {
		return Registration{}, err
	}
	var conversionErr error
	if registration.ID, conversionErr = requiredUUID(id); conversionErr != nil {
		return Registration{}, conversionErr
	}
	if registration.IntentID, conversionErr = requiredUUID(intentID); conversionErr != nil {
		return Registration{}, conversionErr
	}
	if registration.EmailIdentityID, conversionErr = requiredUUID(emailIdentityID); conversionErr != nil {
		return Registration{}, conversionErr
	}
	if registration.PhoneIdentityID, conversionErr = requiredUUID(phoneIdentityID); conversionErr != nil {
		return Registration{}, conversionErr
	}
	registration.EmailChallengeID = optionalUUID(emailChallengeID)
	registration.PhoneChallengeID = optionalUUID(phoneChallengeID)
	registration.Status = Status(status)
	registration.VerifiedMethods = methodsFromStrings(methods)
	registration.VerificationBinding = optionalUUID(verificationBinding)
	registration.VerificationVersion = optionalInt64(verificationVersion)
	registration.VerificationEventID = optionalUUID(verificationEventID)
	registration.LinkRequestID = optionalUUID(linkRequestID)
	registration.CompletionIdempotencyRecordID = optionalUUID(completionID)
	registration.UserID = optionalUUID(userID)
	registration.LinkedAt = optionalTime(linkedAt)
	registration.SessionID = optionalUUID(sessionID)
	registration.SessionPolicyVersion = optionalInt64(sessionPolicyVersion)
	registration.LinkAcceptUntil = optionalTime(linkAcceptUntil)
	registration.SessionIssueUntil = optionalTime(sessionIssueUntil)
	registration.FailureCode = optionalString(failureCode)
	registration.CompletedAt = optionalTime(completedAt)
	registration.StatusTokenHash = copyBytes(registration.StatusTokenHash)
	registration.VerificationHash = copyBytes(registration.VerificationHash)
	registration.StatusTokenExpires = registration.StatusTokenExpires.UTC()
	registration.ExpiresAt = registration.ExpiresAt.UTC()
	registration.CreatedAt = registration.CreatedAt.UTC()
	registration.UpdatedAt = registration.UpdatedAt.UTC()
	return registration, nil
}

func requiredUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, fmt.Errorf("%w: required UUID is null", ErrInvalidRegistration)
	}
	return uuid.UUID(value.Bytes), nil
}

func optionalUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
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

func optionalString(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
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

func nullableStringParam(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func methodsParam(methods []Method) []string {
	if len(methods) == 0 {
		return []string{}
	}
	result := make([]string, len(methods))
	for index, method := range methods {
		result[index] = string(method)
	}
	return result
}

func methodsFromStrings(values []string) []Method {
	result := make([]Method, len(values))
	for index, value := range values {
		result[index] = Method(value)
	}
	return result
}
