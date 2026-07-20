package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type UserSessionRevoker struct {
	tx pgx.Tx
}

func NewUserSessionRevoker(tx pgx.Tx) *UserSessionRevoker {
	return &UserSessionRevoker{tx: tx}
}

func (r *UserSessionRevoker) RevokeForUser(ctx context.Context, userID uuid.UUID, reason string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $2, updated_at = now()
		WHERE user_id = $1 AND session_status = 'active'
	`, userID, reason)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_session_credentials c
		SET credential_status = 'revoked', revoked_at = now()
		FROM auth_sessions s
		WHERE c.session_id = s.session_id AND s.user_id = $1
			AND c.credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, userID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_reauth_proofs p
		SET invalidated_at = now()
		FROM auth_sessions s
		WHERE p.session_id = s.session_id AND s.user_id = $1
			AND p.consumed_at IS NULL AND p.invalidated_at IS NULL
	`, userID)
	return err
}
