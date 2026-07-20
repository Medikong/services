package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	applicationregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type RegistrationRepository struct {
	tx pgx.Tx
}

func NewRegistrationRepository(tx pgx.Tx) *RegistrationRepository {
	return &RegistrationRepository{tx: tx}
}

func (r *RegistrationRepository) Create(ctx context.Context, registration domainregistration.Registration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	_, err := r.tx.Exec(ctx, `
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
		registrationUUIDParam(registration.ID),
		registrationUUIDParam(registration.IntentID),
		registrationUUIDParam(registration.EmailIdentityID),
		registrationUUIDParam(registration.PhoneIdentityID),
		registrationNullableUUIDParam(registration.EmailChallengeID),
		registrationNullableUUIDParam(registration.PhoneChallengeID),
		registration.ProfileRequestID,
		registration.AgreementReceiptID,
		registration.RememberMe,
		registration.ClientChannel,
		string(registration.Status),
		registrationMethodsParam(registration.VerifiedMethods),
		registration.StatusTokenHash,
		registration.StatusTokenKeyVer,
		registration.StatusTokenExpires,
		registrationNullableUUIDParam(registration.VerificationBinding),
		registrationNullableInt64Param(registration.VerificationVersion),
		registrationNullableBytes(registration.VerificationHash),
		registrationNullableUUIDParam(registration.VerificationEventID),
		registrationNullableUUIDParam(registration.LinkRequestID),
		registrationNullableUUIDParam(registration.CompletionIdempotencyRecordID),
		registrationNullableUUIDParam(registration.UserID),
		registrationNullableTimeParam(registration.LinkedAt),
		registrationNullableUUIDParam(registration.SessionID),
		registrationNullableInt64Param(registration.SessionPolicyVersion),
		registrationNullableTimeParam(registration.LinkAcceptUntil),
		registrationNullableTimeParam(registration.SessionIssueUntil),
		registrationNullableStringParam(registration.FailureCode),
		registration.ExpiresAt,
		registrationNullableTimeParam(registration.CompletedAt),
		registration.Version,
		registration.CreatedAt,
		registration.UpdatedAt,
	)
	return err
}

func (r *RegistrationRepository) Find(ctx context.Context, registrationID uuid.UUID) (domainregistration.Registration, error) {
	registration, err := scanRegistration(r.tx.QueryRow(ctx, registrationSelect+` WHERE registration_id = $1`, registrationUUIDParam(registrationID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domainregistration.Registration{}, domainregistration.ErrNotFound
	}
	return registration, err
}

func (r *RegistrationRepository) FindForUpdate(ctx context.Context, registrationID uuid.UUID) (domainregistration.Registration, error) {
	registration, err := scanRegistration(r.tx.QueryRow(ctx, registrationSelect+` WHERE registration_id = $1 FOR UPDATE`, registrationUUIDParam(registrationID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domainregistration.Registration{}, domainregistration.ErrNotFound
	}
	return registration, err
}

func (r *RegistrationRepository) Save(ctx context.Context, registration *domainregistration.Registration) error {
	if registration == nil {
		return domainregistration.ErrInvalidRegistration
	}
	if err := registration.Validate(); err != nil {
		return err
	}
	expectedVersion := registration.Version
	var version int64
	var updatedAt time.Time
	err := r.tx.QueryRow(ctx, `
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
		registrationUUIDParam(registration.ID),
		registrationNullableUUIDParam(registration.EmailChallengeID),
		registrationNullableUUIDParam(registration.PhoneChallengeID),
		registration.RememberMe,
		registration.ClientChannel,
		string(registration.Status),
		registrationMethodsParam(registration.VerifiedMethods),
		registration.StatusTokenHash,
		registration.StatusTokenKeyVer,
		registration.StatusTokenExpires,
		registrationNullableUUIDParam(registration.VerificationBinding),
		registrationNullableInt64Param(registration.VerificationVersion),
		registrationNullableBytes(registration.VerificationHash),
		registrationNullableUUIDParam(registration.VerificationEventID),
		registrationNullableUUIDParam(registration.LinkRequestID),
		registrationNullableUUIDParam(registration.CompletionIdempotencyRecordID),
		registrationNullableUUIDParam(registration.UserID),
		registrationNullableTimeParam(registration.LinkedAt),
		registrationNullableUUIDParam(registration.SessionID),
		registrationNullableInt64Param(registration.SessionPolicyVersion),
		registrationNullableTimeParam(registration.LinkAcceptUntil),
		registrationNullableTimeParam(registration.SessionIssueUntil),
		registrationNullableStringParam(registration.FailureCode),
		registration.ExpiresAt,
		registrationNullableTimeParam(registration.CompletedAt),
		expectedVersion,
	).Scan(&version, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainregistration.ErrVersionConflict
	}
	if err != nil {
		return err
	}
	registration.Version = version
	registration.UpdatedAt = updatedAt.UTC()
	return nil
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

func scanRegistration(row pgx.Row) (domainregistration.Registration, error) {
	var (
		id, intentID, emailIdentityID, phoneIdentityID                               pgtype.UUID
		emailChallengeID, phoneChallengeID, verificationBinding, verificationEventID pgtype.UUID
		linkRequestID, completionID, userID, sessionID                               pgtype.UUID
		verificationVersion, sessionPolicyVersion                                    pgtype.Int8
		linkedAt, linkAcceptUntil, sessionIssueUntil, completedAt                    pgtype.Timestamptz
		failureCode                                                                  pgtype.Text
		status                                                                       string
		methods                                                                      []string
		registration                                                                 domainregistration.Registration
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
		return domainregistration.Registration{}, err
	}
	var conversionErr error
	if registration.ID, conversionErr = registrationRequiredUUID(id); conversionErr != nil {
		return domainregistration.Registration{}, conversionErr
	}
	if registration.IntentID, conversionErr = registrationRequiredUUID(intentID); conversionErr != nil {
		return domainregistration.Registration{}, conversionErr
	}
	if registration.EmailIdentityID, conversionErr = registrationRequiredUUID(emailIdentityID); conversionErr != nil {
		return domainregistration.Registration{}, conversionErr
	}
	if registration.PhoneIdentityID, conversionErr = registrationRequiredUUID(phoneIdentityID); conversionErr != nil {
		return domainregistration.Registration{}, conversionErr
	}
	registration.EmailChallengeID = registrationOptionalUUID(emailChallengeID)
	registration.PhoneChallengeID = registrationOptionalUUID(phoneChallengeID)
	registration.Status = domainregistration.Status(status)
	registration.VerifiedMethods = registrationMethodsFromStrings(methods)
	registration.VerificationBinding = registrationOptionalUUID(verificationBinding)
	registration.VerificationVersion = registrationOptionalInt64(verificationVersion)
	registration.VerificationEventID = registrationOptionalUUID(verificationEventID)
	registration.LinkRequestID = registrationOptionalUUID(linkRequestID)
	registration.CompletionIdempotencyRecordID = registrationOptionalUUID(completionID)
	registration.UserID = registrationOptionalUUID(userID)
	registration.LinkedAt = registrationOptionalTime(linkedAt)
	registration.SessionID = registrationOptionalUUID(sessionID)
	registration.SessionPolicyVersion = registrationOptionalInt64(sessionPolicyVersion)
	registration.LinkAcceptUntil = registrationOptionalTime(linkAcceptUntil)
	registration.SessionIssueUntil = registrationOptionalTime(sessionIssueUntil)
	registration.FailureCode = registrationOptionalString(failureCode)
	registration.CompletedAt = registrationOptionalTime(completedAt)
	registration.StatusTokenHash = append([]byte(nil), registration.StatusTokenHash...)
	registration.VerificationHash = append([]byte(nil), registration.VerificationHash...)
	registration.StatusTokenExpires = registration.StatusTokenExpires.UTC()
	registration.ExpiresAt = registration.ExpiresAt.UTC()
	registration.CreatedAt = registration.CreatedAt.UTC()
	registration.UpdatedAt = registration.UpdatedAt.UTC()
	return registration, nil
}

func registrationRequiredUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, fmt.Errorf("%w: required UUID is null", domainregistration.ErrInvalidRegistration)
	}
	return uuid.UUID(value.Bytes), nil
}

func registrationOptionalUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}

func registrationOptionalInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func registrationOptionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func registrationOptionalString(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func registrationUUIDParam(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func registrationNullableUUIDParam(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return registrationUUIDParam(*value)
}

func registrationNullableInt64Param(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func registrationNullableTimeParam(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func registrationNullableStringParam(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func registrationNullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func registrationMethodsParam(methods []domainregistration.Method) []string {
	if len(methods) == 0 {
		return []string{}
	}
	result := make([]string, len(methods))
	for index, method := range methods {
		result[index] = string(method)
	}
	return result
}

func registrationMethodsFromStrings(values []string) []domainregistration.Method {
	result := make([]domainregistration.Method, len(values))
	for index, value := range values {
		result[index] = domainregistration.Method(value)
	}
	return result
}

var _ applicationregistration.Repository = (*RegistrationRepository)(nil)
