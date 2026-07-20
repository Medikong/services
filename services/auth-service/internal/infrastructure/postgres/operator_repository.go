package postgres

import (
	"context"
	"errors"

	domainoperator "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type OperatorRepository struct {
	tx pgx.Tx
}

func NewOperatorRepository(tx pgx.Tx) *OperatorRepository {
	return &OperatorRepository{tx: tx}
}

func (r *OperatorRepository) GetUser(ctx context.Context, userID uuid.UUID) (domainoperator.UserView, error) {
	var view domainoperator.UserView
	err := r.tx.QueryRow(ctx, `
		SELECT user_id, status, row_version
		FROM auth_user_auth_states
		WHERE user_id = $1
	`, userID).Scan(&view.UserID, &view.Status, &view.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainoperator.UserView{}, domainoperator.ErrNotFound
	}
	if err != nil {
		return domainoperator.UserView{}, err
	}
	rows, err := r.tx.Query(ctx, `
		SELECT i.identity_id, l.identity_link_id, i.identity_type, i.masked_value,
			i.verification_status, l.link_status, i.row_version,
			(i.credential_status = 'locked'), i.lock_until
		FROM auth_identity_links l
		JOIN auth_identities i ON i.identity_id = l.identity_id
		WHERE l.user_id = $1
		ORDER BY i.identity_type
	`, userID)
	if err != nil {
		return domainoperator.UserView{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var identity domainoperator.IdentityView
		if err := rows.Scan(
			&identity.IdentityID, &identity.LinkID, &identity.Type, &identity.MaskedValue,
			&identity.VerificationStatus, &identity.LinkStatus, &identity.RowVersion,
			&identity.Locked, &identity.UnlockAvailableAt,
		); err != nil {
			return domainoperator.UserView{}, err
		}
		view.Identities = append(view.Identities, identity)
	}
	if err := rows.Err(); err != nil {
		return domainoperator.UserView{}, err
	}
	if err := r.tx.QueryRow(ctx, `
		SELECT count(*)
		FROM auth_sessions
		WHERE user_id = $1 AND session_status = 'active'
	`, userID).Scan(&view.ActiveSessions); err != nil {
		return domainoperator.UserView{}, err
	}
	return view, nil
}

func (r *OperatorRepository) ApplyManual(ctx context.Context, input domainoperator.ManualAction) (int64, error) {
	if err := input.Validate(); err != nil {
		return 0, err
	}
	var version int64
	var err error
	switch input.Action {
	case "unlock_identity":
		err = r.tx.QueryRow(ctx, `
			UPDATE auth_identities
			SET credential_status = 'active', lock_until = NULL, lock_policy_version = NULL,
				row_version = row_version + 1, updated_at = now()
			WHERE identity_id = $1 AND row_version = $2
			RETURNING row_version
		`, input.TargetID, input.ExpectedVersion).Scan(&version)
	case "revoke_identity_link":
		err = r.tx.QueryRow(ctx, `
			UPDATE auth_identity_links
			SET link_status = 'revoked', closed_at = now(), closed_reason = $3,
				row_version = row_version + 1, updated_at = now()
			WHERE identity_link_id = $1 AND row_version = $2
			RETURNING row_version
		`, input.TargetID, input.ExpectedVersion, input.ReasonCode).Scan(&version)
	case "approve_relink":
		err = r.tx.QueryRow(ctx, `
			UPDATE auth_identity_links
			SET link_status = 'active', activated_at = now(), row_version = row_version + 1, updated_at = now()
			WHERE identity_link_id = $1 AND link_status = 'requested' AND row_version = $2
			RETURNING row_version
		`, input.TargetID, input.ExpectedVersion).Scan(&version)
	case "revoke_sessions":
		err = r.tx.QueryRow(ctx, `
			UPDATE auth_sessions
			SET session_status = 'revoked', revoked_at = now(), revocation_reason = $3,
				row_version = row_version + 1, updated_at = now()
			WHERE session_id = $1 AND row_version = $2
			RETURNING row_version
		`, input.TargetID, input.ExpectedVersion, input.ReasonCode).Scan(&version)
		if err == nil {
			_, err = r.tx.Exec(ctx, `
				UPDATE auth_session_credentials
				SET credential_status = 'revoked', revoked_at = now()
				WHERE session_id = $1
					AND credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
			`, input.TargetID)
		}
	default:
		return 0, domainoperator.ErrInvalidManualAction
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domainoperator.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	_, err = r.tx.Exec(ctx, `
		INSERT INTO auth_manual_actions (
			manual_action_id, operator_user_id, case_id, target_type, target_id,
			action, reason_code, approval_id, evidence_ref, expected_target_version,
			target_version, status, idempotency_record_id, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'completed', $12, now())
	`, input.ID, input.OperatorID, input.CaseID, input.TargetType, input.TargetID,
		input.Action, input.ReasonCode, input.ApprovalID, input.EvidenceRef,
		input.ExpectedVersion, version, input.IdempotencyID)
	if err != nil {
		return 0, err
	}
	return version, nil
}

func (r *OperatorRepository) FindManualResult(ctx context.Context, actionID uuid.UUID) (domainoperator.ManualResult, error) {
	var result domainoperator.ManualResult
	err := r.tx.QueryRow(ctx, `
		SELECT manual_action_id, target_version
		FROM auth_manual_actions
		WHERE manual_action_id = $1 AND status = 'completed'
	`, actionID).Scan(&result.ActionID, &result.TargetVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainoperator.ManualResult{}, domainoperator.ErrNotFound
	}
	return result, err
}

var _ domainoperator.Repository = (*OperatorRepository)(nil)
