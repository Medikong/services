package session

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

func (r *PostgresRepository) UseStatusProjection(status *StatusService) {
	r.status = status
}

func (r *PostgresRepository) ProjectRevoked(ctx context.Context, sessionID uuid.UUID) error {
	if r.status == nil {
		return nil
	}
	return r.status.Project(ctx, sessionID)
}

func (r *PostgresRepository) ProjectRevokedForUser(ctx context.Context, userID uuid.UUID) error {
	if r.status == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT session_id FROM auth_sessions
		WHERE user_id = $1 AND session_status <> 'active'
		ORDER BY session_id
	`, userID)
	if err != nil {
		return oops.In("session_status_projection").Code("user.query_failed").Wrap(err)
	}
	return r.projectStatusRows(ctx, rows)
}

func (r *PostgresRepository) ProjectRevokedForIdentityLinkExcept(ctx context.Context, identityLinkID, keepSessionID uuid.UUID) error {
	if r.status == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT session_id FROM auth_sessions
		WHERE identity_link_id = $1 AND session_id <> $2 AND session_status <> 'active'
		ORDER BY session_id
	`, identityLinkID, keepSessionID)
	if err != nil {
		return oops.In("session_status_projection").Code("identity_link.query_failed").Wrap(err)
	}
	return r.projectStatusRows(ctx, rows)
}

func (r *PostgresRepository) projectStatusRows(ctx context.Context, rows pgx.Rows) error {
	defer rows.Close()
	for rows.Next() {
		var sessionID uuid.UUID
		if err := rows.Scan(&sessionID); err != nil {
			return oops.In("session_status_projection").Code("rows.scan_failed").Wrap(err)
		}
		if err := r.status.Project(ctx, sessionID); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return oops.In("session_status_projection").Code("rows.read_failed").Wrap(err)
	}
	return nil
}
