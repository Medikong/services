package access

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("access state not found")

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateActiveForRegistration(ctx context.Context, tx pgx.Tx, userID, sourceEventID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_user_auth_states (
			user_id, status, restriction_version, source_event_id, effective_at, updated_at
		) VALUES ($1, 'active', 1, $2, now(), now())
		ON CONFLICT (user_id) DO NOTHING
	`, userID, sourceEventID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_access_grants (
			access_grant_id, user_id, roles, permissions, grant_version, grant_status,
			claims_hash, source, source_revision, valid_from, created_at, updated_at
		) VALUES ($3, $1, ARRAY['customer'], ARRAY[]::text[], 1, 'active',
			NULL, 'registration', $2::text, now(), now(), now())
		ON CONFLICT (user_id) WHERE grant_status = 'active' DO NOTHING
	`, userID, sourceEventID, uuid.New())
	return err
}

func (r *PostgresRepository) FindActiveForUpdate(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (State, Grant, error) {
	return r.find(ctx, tx, userID, true)
}

func (r *PostgresRepository) FindActive(ctx context.Context, userID uuid.UUID) (State, Grant, error) {
	return r.find(ctx, r.pool, userID, false)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *PostgresRepository) find(ctx context.Context, q queryer, userID uuid.UUID, forUpdate bool) (State, Grant, error) {
	sql := `
		SELECT s.user_id, s.status, s.restriction_version,
			g.access_grant_id, g.user_id, g.roles, g.permissions, g.grant_version, g.grant_status
		FROM auth_user_auth_states s
		JOIN auth_access_grants g ON g.user_id = s.user_id AND g.grant_status = 'active'
		WHERE s.user_id = $1
	`
	if forUpdate {
		sql += " FOR UPDATE"
	}
	var state State
	var grant Grant
	err := q.QueryRow(ctx, sql, userID).Scan(
		&state.UserID, &state.Status, &state.RestrictionVersion,
		&grant.ID, &grant.UserID, &grant.Roles, &grant.Permissions, &grant.Version, &grant.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, Grant{}, ErrNotFound
	}
	return state, grant, err
}

func (r *PostgresRepository) Restrict(ctx context.Context, tx pgx.Tx, userID uuid.UUID, reason string, version int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_user_auth_states
		SET status = 'restricted', reason_code = $2, restriction_version = $3, effective_at = now(),
			row_version = row_version + 1, updated_at = now()
		WHERE user_id = $1 AND restriction_version < $3
	`, userID, reason, version)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_access_grants
		SET grant_status = 'revoked', revoked_at = now(), updated_at = now()
		WHERE user_id = $1 AND grant_status = 'active'
	`, userID)
	return err
}
