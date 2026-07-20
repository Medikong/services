package postgres

import (
	"context"
	"errors"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SessionUserAuthStateReader struct {
	tx pgx.Tx
}

func NewSessionUserAuthStateReader(tx pgx.Tx) *SessionUserAuthStateReader {
	return &SessionUserAuthStateReader{tx: tx}
}

func (r *SessionUserAuthStateReader) FindForUpdate(ctx context.Context, userID uuid.UUID) (domainuserauthstate.State, error) {
	var state domainuserauthstate.State
	err := r.tx.QueryRow(ctx, `
		SELECT user_id, status, user_version, status_change_id, effective_at, row_version
		FROM auth_user_auth_states
		WHERE user_id = $1
		FOR UPDATE
	`, userID).Scan(&state.UserID, &state.Status, &state.UserVersion, &state.StatusChangeID, &state.EffectiveAt, &state.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainuserauthstate.State{}, domainuserauthstate.ErrNotFound
	}
	return state, err
}

var _ applicationsession.UserAuthStateReader = (*SessionUserAuthStateReader)(nil)
