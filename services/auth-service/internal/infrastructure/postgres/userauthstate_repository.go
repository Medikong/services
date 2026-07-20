package postgres

import (
	"context"
	"errors"
	"strings"

	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type UserAuthStateRepository struct {
	tx pgx.Tx
}

func NewUserAuthStateRepository(tx pgx.Tx) *UserAuthStateRepository {
	return &UserAuthStateRepository{tx: tx}
}

func (r *UserAuthStateRepository) CreateActiveForRegistration(ctx context.Context, userID uuid.UUID, userVersion int64, statusChangeID string) error {
	if userVersion < 1 || strings.TrimSpace(statusChangeID) == "" {
		return errors.New("invalid initial user auth state")
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_user_auth_states (
			user_id, status, user_version, status_change_id, effective_at, updated_at
		) VALUES ($1, 'active', $2, $3, now(), now())
		ON CONFLICT (user_id) DO NOTHING
	`, userID, userVersion, statusChangeID)
	return err
}

func (r *UserAuthStateRepository) FindForUpdate(ctx context.Context, userID uuid.UUID) (domainuserauthstate.State, error) {
	var state domainuserauthstate.State
	err := r.tx.QueryRow(ctx, `
		SELECT user_id, status, user_version, status_change_id, effective_at, row_version
		FROM auth_user_auth_states
		WHERE user_id = $1
		FOR UPDATE
	`, userID).Scan(
		&state.UserID, &state.Status, &state.UserVersion,
		&state.StatusChangeID, &state.EffectiveAt, &state.RowVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainuserauthstate.State{}, domainuserauthstate.ErrNotFound
	}
	return state, err
}

func (r *UserAuthStateRepository) Apply(ctx context.Context, current domainuserauthstate.State, change domainuserauthstate.Change) (domainuserauthstate.State, error) {
	result, err := r.tx.Exec(ctx, `
		UPDATE auth_user_auth_states
		SET status = $2, user_version = $3, status_change_id = $4,
			reason_code = 'USER_ACCOUNT_STATUS_CHANGED', effective_at = $5,
			row_version = row_version + 1, updated_at = now()
		WHERE user_id = $1 AND row_version = $6 AND user_version < $3
	`, current.UserID, change.Status, change.UserVersion, change.StatusChangeID, change.ChangedAt, current.RowVersion)
	if err != nil {
		return domainuserauthstate.State{}, err
	}
	if result.RowsAffected() != 1 {
		return domainuserauthstate.State{}, domainuserauthstate.ErrVersionConflict
	}
	current.Status = change.Status
	current.UserVersion = change.UserVersion
	current.StatusChangeID = change.StatusChangeID
	current.EffectiveAt = change.ChangedAt
	current.RowVersion++
	return current, nil
}

var _ domainuserauthstate.Repository = (*UserAuthStateRepository)(nil)
