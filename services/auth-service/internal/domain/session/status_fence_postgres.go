package session

import (
	"context"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

func (r *PostgresRepository) FenceRevocation(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (domain.RevocationFences, error) {
	if r.status == nil {
		return StatusFenceSet{}, nil
	}
	rows, err := tx.Query(ctx, fenceStatusSelect+` AND session_id = $1 FOR UPDATE`, sessionID)
	if err != nil {
		return nil, oops.In("session_status_fence").Code("session.query_failed").Wrap(err)
	}
	return r.fenceStatusRows(ctx, rows)
}

func (r *PostgresRepository) FenceRevocationsForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (domain.RevocationFences, error) {
	if r.status == nil {
		return StatusFenceSet{}, nil
	}
	rows, err := tx.Query(ctx, fenceStatusSelect+` AND user_id = $1 ORDER BY session_id FOR UPDATE`, userID)
	if err != nil {
		return nil, oops.In("session_status_fence").Code("user.query_failed").Wrap(err)
	}
	return r.fenceStatusRows(ctx, rows)
}

func (r *PostgresRepository) FenceRevocationsForIdentityLinkExcept(ctx context.Context, tx pgx.Tx, scope IdentityLinkRevocationScope) (domain.RevocationFences, error) {
	if r.status == nil {
		return StatusFenceSet{}, nil
	}
	rows, err := tx.Query(ctx, fenceStatusSelect+` AND identity_link_id = $1 AND session_id <> $2 ORDER BY session_id FOR UPDATE`, scope.IdentityLinkID, scope.KeepSessionID)
	if err != nil {
		return nil, oops.In("session_status_fence").Code("identity_link.query_failed").Wrap(err)
	}
	return r.fenceStatusRows(ctx, rows)
}

const fenceStatusSelect = `
	SELECT user_id, session_id, idle_expires_at, absolute_expires_at, row_version
	FROM auth_sessions
	WHERE session_status = 'active'`

func (r *PostgresRepository) fenceStatusRows(ctx context.Context, rows pgx.Rows) (domain.RevocationFences, error) {
	defer rows.Close()
	set := StatusFenceSet{status: r.status}
	for rows.Next() {
		var record StatusRecord
		if err := rows.Scan(&record.UserID, &record.SessionID, &record.IdleExpiresAt, &record.AbsoluteExpiresAt, &record.Version); err != nil {
			return set, oops.In("session_status_fence").Code("rows.scan_failed").Wrap(err)
		}
		record.State = StatusActive
		fence, err := r.status.fence(ctx, record)
		if err != nil {
			return set, err
		}
		set.fences = append(set.fences, fence)
	}
	if err := rows.Err(); err != nil {
		return set, oops.In("session_status_fence").Code("rows.read_failed").Wrap(err)
	}
	return set, nil
}
