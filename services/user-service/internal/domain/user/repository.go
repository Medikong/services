package user

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type UserRepository struct {
	db *pgxpool.Pool
}

type IdempotencyRecord struct {
	Operation     string
	ScopeID       string
	Key           string
	RequestHash   []byte
	ResultType    *string
	ResultID      *string
	ResultVersion *int64
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type CreateInput struct {
	UserID                uuid.UUID
	RegistrationID        string
	PrivateNameCiphertext []byte
	Nickname              string
	Introduction          *string
	Agreements            []AgreementAcceptance
	Now                   time.Time
}

type MutationResult struct {
	UserID    uuid.UUID
	Version   int64
	UpdatedAt time.Time
}

type StatusMutationResult struct {
	MutationResult
	PreviousStatus AccountStatus
	ChangedStatus  AccountStatus
	StatusChangeID uuid.UUID
	ChangedAt      time.Time
}

type StatusHistory struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	PreviousStatus AccountStatus
	ChangedStatus  AccountStatus
	ReasonCode     string
	ChangedBy      string
	ChangedAt      time.Time
}

func NewUserRepository(db *pgxpool.Pool) (*UserRepository, error) {
	if db == nil {
		return nil, oops.In("user_repository").Code("user.database_required").New("postgres pool is required")
	}
	return &UserRepository{db: db}, nil
}

func (r *UserRepository) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, oops.In("user_repository").Code("user.transaction_begin_failed").Wrap(err)
	}
	return tx, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	return scanUser(r.db.QueryRow(ctx, userByIDQuery, id))
}

func (r *UserRepository) GetByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (User, error) {
	return scanUser(tx.QueryRow(ctx, userByIDQuery, id))
}

const userByIDQuery = `
	SELECT user_id, registration_id, account_status, nickname, introduction,
	       profile_media_asset_id, user_version, created_at, updated_at
	FROM users
	WHERE user_id = $1
`

func scanUser(row pgx.Row) (User, error) {
	var current User
	err := row.Scan(
		&current.ID,
		&current.RegistrationID,
		&current.AccountStatus,
		&current.Nickname,
		&current.Introduction,
		&current.ProfileMediaAssetID,
		&current.Version,
		&current.CreatedAt,
		&current.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, oops.In("user_repository").Code("user.query_failed").Wrap(err)
	}
	return current, nil
}

func (r *UserRepository) ClaimIdempotency(ctx context.Context, tx pgx.Tx, record IdempotencyRecord) (IdempotencyRecord, bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO user_idempotency_records (
			operation, scope_id, idempotency_key, request_hash, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (operation, scope_id, idempotency_key) DO NOTHING
	`, record.Operation, record.ScopeID, record.Key, record.RequestHash, record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return IdempotencyRecord{}, false, oops.In("user_repository").Code("user.idempotency_claim_failed").Wrap(err)
	}
	if tag.RowsAffected() == 1 {
		return record, false, nil
	}

	var current IdempotencyRecord
	err = tx.QueryRow(ctx, `
		SELECT operation, scope_id, idempotency_key, request_hash,
		       result_type, result_id, result_version, created_at, expires_at
		FROM user_idempotency_records
		WHERE operation = $1 AND scope_id = $2 AND idempotency_key = $3
	`, record.Operation, record.ScopeID, record.Key).Scan(
		&current.Operation,
		&current.ScopeID,
		&current.Key,
		&current.RequestHash,
		&current.ResultType,
		&current.ResultID,
		&current.ResultVersion,
		&current.CreatedAt,
		&current.ExpiresAt,
	)
	if err != nil {
		return IdempotencyRecord{}, false, oops.In("user_repository").Code("user.idempotency_query_failed").Wrap(err)
	}
	if !bytes.Equal(current.RequestHash, record.RequestHash) || current.ResultType == nil || current.ResultID == nil || current.ResultVersion == nil {
		return IdempotencyRecord{}, false, ErrIdempotencyConflict
	}
	return current, true, nil
}

func (r *UserRepository) CompleteIdempotency(ctx context.Context, tx pgx.Tx, record IdempotencyRecord, resultType, resultID string, resultVersion int64) error {
	tag, err := tx.Exec(ctx, `
		UPDATE user_idempotency_records
		SET result_type = $4, result_id = $5, result_version = $6
		WHERE operation = $1 AND scope_id = $2 AND idempotency_key = $3
		  AND result_type IS NULL
	`, record.Operation, record.ScopeID, record.Key, resultType, resultID, resultVersion)
	if err != nil {
		return oops.In("user_repository").Code("user.idempotency_complete_failed").Wrap(err)
	}
	if tag.RowsAffected() != 1 {
		return oops.In("user_repository").Code("user.idempotency_record_invalid").New("idempotency record was not exclusively completed")
	}
	return nil
}

func (r *UserRepository) Create(ctx context.Context, tx pgx.Tx, input CreateInput) (User, error) {
	current := User{
		ID:             input.UserID,
		RegistrationID: input.RegistrationID,
		AccountStatus:  StatusActive,
		Nickname:       input.Nickname,
		Introduction:   input.Introduction,
		Version:        1,
		CreatedAt:      input.Now,
		UpdatedAt:      input.Now,
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO users (
			user_id, registration_id, account_status, private_name_ciphertext,
			nickname, introduction, user_version, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)
	`, current.ID, current.RegistrationID, current.AccountStatus, input.PrivateNameCiphertext, current.Nickname, current.Introduction, input.Now)
	if err != nil {
		return User{}, oops.In("user_repository").Code("user.create_failed").Wrap(err)
	}
	for _, agreement := range input.Agreements {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_agreement_acceptances (
				user_id, agreement_code, agreement_version, accepted_at
			) VALUES ($1, $2, $3, $4)
		`, current.ID, agreement.Code, agreement.Version, agreement.AcceptedAt); err != nil {
			return User{}, oops.In("user_repository").Code("user.agreement_create_failed").Wrap(err)
		}
	}
	return current, nil
}

func (r *UserRepository) UpdateProfile(ctx context.Context, tx pgx.Tx, id uuid.UUID, expectedVersion int64, patch ProfilePatch, now time.Time) (MutationResult, error) {
	nickname := patch.Nickname
	var result MutationResult
	err := tx.QueryRow(ctx, `
		UPDATE users
		SET nickname = CASE WHEN $2 THEN $3 ELSE nickname END,
		    introduction = CASE WHEN $4 THEN $5 ELSE introduction END,
		    user_version = user_version + 1,
		    updated_at = $6
		WHERE user_id = $1
		  AND user_version = $7
		  AND account_status = 'active'
		RETURNING user_id, user_version, updated_at
	`, id, patch.NicknameSet, nickname, patch.IntroductionSet, patch.Introduction, now, expectedVersion).Scan(
		&result.UserID,
		&result.Version,
		&result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, r.classifyActiveMutation(ctx, tx, id, expectedVersion)
	}
	if err != nil {
		return MutationResult{}, oops.In("user_repository").Code("user.profile_update_failed").Wrap(err)
	}
	return result, nil
}

func (r *UserRepository) UpdateProfileImage(ctx context.Context, tx pgx.Tx, id uuid.UUID, mediaAssetID string, expectedVersion int64, now time.Time) (MutationResult, error) {
	var result MutationResult
	err := tx.QueryRow(ctx, `
		UPDATE users
		SET profile_media_asset_id = $2,
		    user_version = user_version + 1,
		    updated_at = $3
		WHERE user_id = $1
		  AND user_version = $4
		  AND account_status = 'active'
		RETURNING user_id, user_version, updated_at
	`, id, mediaAssetID, now, expectedVersion).Scan(&result.UserID, &result.Version, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, r.classifyActiveMutation(ctx, tx, id, expectedVersion)
	}
	if err != nil {
		return MutationResult{}, oops.In("user_repository").Code("user.profile_image_update_failed").Wrap(err)
	}
	return result, nil
}

func (r *UserRepository) ChangeStatus(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	target AccountStatus,
	expectedVersion int64,
	statusChangeID uuid.UUID,
	reasonCode string,
	changedBy string,
	now time.Time,
) (StatusMutationResult, error) {
	allowedPrevious := AllowedPreviousStatuses(target)
	var result StatusMutationResult
	err := tx.QueryRow(ctx, `
		WITH candidate AS MATERIALIZED (
			SELECT user_id, account_status AS previous_status
			FROM users
			WHERE user_id = $1
			  AND user_version = $2
			  AND account_status = ANY($3::text[])
		), updated AS (
			UPDATE users AS current
			SET account_status = $4,
			    user_version = current.user_version + 1,
			    updated_at = $5
			FROM candidate
			WHERE current.user_id = candidate.user_id
			  AND current.user_version = $2
			  AND current.account_status = candidate.previous_status
			RETURNING current.user_id, current.user_version, current.updated_at, candidate.previous_status
		)
		SELECT user_id, user_version, updated_at, previous_status
		FROM updated
	`, id, expectedVersion, allowedPrevious, target, now).Scan(
		&result.UserID,
		&result.Version,
		&result.UpdatedAt,
		&result.PreviousStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		current, queryErr := r.GetByIDTx(ctx, tx, id)
		if queryErr != nil {
			return StatusMutationResult{}, queryErr
		}
		if current.Version != expectedVersion {
			return StatusMutationResult{}, ErrVersionConflict
		}
		return StatusMutationResult{}, ErrTransitionInvalid
	}
	if err != nil {
		return StatusMutationResult{}, oops.In("user_repository").Code("user.status_update_failed").Wrap(err)
	}
	result.ChangedStatus = target
	result.StatusChangeID = statusChangeID
	result.ChangedAt = now
	_, err = tx.Exec(ctx, `
		INSERT INTO user_status_history (
			status_change_id, user_id, previous_status, changed_status,
			reason_code, changed_by, changed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, statusChangeID, id, result.PreviousStatus, target, reasonCode, changedBy, now)
	if err != nil {
		return StatusMutationResult{}, oops.In("user_repository").Code("user.status_history_create_failed").Wrap(err)
	}
	return result, nil
}

func (r *UserRepository) GetStatusHistory(ctx context.Context, tx pgx.Tx, id uuid.UUID) (StatusHistory, error) {
	var history StatusHistory
	err := tx.QueryRow(ctx, `
		SELECT status_change_id, user_id, previous_status, changed_status,
		       reason_code, changed_by, changed_at
		FROM user_status_history
		WHERE status_change_id = $1
	`, id).Scan(
		&history.ID,
		&history.UserID,
		&history.PreviousStatus,
		&history.ChangedStatus,
		&history.ReasonCode,
		&history.ChangedBy,
		&history.ChangedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StatusHistory{}, ErrNotFound
	}
	if err != nil {
		return StatusHistory{}, oops.In("user_repository").Code("user.status_history_query_failed").Wrap(err)
	}
	return history, nil
}

func (r *UserRepository) classifyActiveMutation(ctx context.Context, tx pgx.Tx, id uuid.UUID, expectedVersion int64) error {
	current, err := r.GetByIDTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if current.Version != expectedVersion {
		return ErrVersionConflict
	}
	if current.AccountStatus != StatusActive {
		return ErrAccountNotActive
	}
	return oops.In("user_repository").Code("user.conditional_update_failed").
		Errorf("user %s matched no conditional update classification", id)
}
