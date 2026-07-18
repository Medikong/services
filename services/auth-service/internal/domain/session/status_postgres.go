package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresStatusSource struct {
	pool      *pgxpool.Pool
	accessTTL time.Duration
}

func NewPostgresStatusSource(pool *pgxpool.Pool, accessTTL time.Duration) *PostgresStatusSource {
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	return &PostgresStatusSource{pool: pool, accessTTL: accessTTL}
}

type postgresStatusRow struct {
	UserID            uuid.UUID
	SessionID         uuid.UUID
	SessionState      string
	UserState         string
	IdleExpiresAt     *time.Time
	AbsoluteExpiresAt time.Time
	Version           int64
	RevokedAt         *time.Time
}

func (s *PostgresStatusSource) FindStatus(ctx context.Context, sessionID uuid.UUID) (StatusRecord, error) {
	var row postgresStatusRow
	err := s.pool.QueryRow(ctx, `
		SELECT s.user_id, s.session_id, s.session_status, uas.status,
			s.idle_expires_at, s.absolute_expires_at, s.row_version,
			CASE
				WHEN s.session_status = 'active' AND uas.status = 'active' THEN NULL
				ELSE COALESCE(s.revoked_at, s.reuse_detected_at, uas.effective_at, s.updated_at)
			END
		FROM auth_sessions s
		JOIN auth_user_auth_states uas ON uas.user_id = s.user_id
		WHERE s.session_id = $1
	`, sessionID).Scan(
		&row.UserID, &row.SessionID, &row.SessionState, &row.UserState,
		&row.IdleExpiresAt, &row.AbsoluteExpiresAt, &row.Version, &row.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StatusRecord{}, ErrStatusNotFound
	}
	if err != nil {
		return StatusRecord{}, oops.In("session_status_source").Code("postgres.read_failed").Wrap(err)
	}
	return statusRecordFromPostgres(row, s.accessTTL), nil
}

func statusRecordFromPostgres(row postgresStatusRow, accessTTL time.Duration) StatusRecord {
	state := StatusRevoked
	switch {
	case row.SessionState == "active" && row.UserState == "active":
		state = StatusActive
	case row.SessionState == "expired":
		state = StatusExpired
	}
	var revokedUntil *time.Time
	if state == StatusRevoked && row.RevokedAt != nil {
		value := row.RevokedAt.Add(accessTTL)
		revokedUntil = &value
	}
	return StatusRecord{
		UserID: row.UserID, SessionID: row.SessionID, State: state,
		IdleExpiresAt: row.IdleExpiresAt, AbsoluteExpiresAt: row.AbsoluteExpiresAt,
		Version: row.Version, RevokedUntil: revokedUntil,
	}
}
