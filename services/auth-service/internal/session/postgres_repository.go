package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/autherror"
	"github.com/Medikong/services/services/auth-service/internal/postgres"
)

type PostgresRepository struct {
	exec postgres.Executor
}

func NewPostgresRepository(exec postgres.Executor) PostgresRepository {
	return PostgresRepository{exec: exec}
}

func (r PostgresRepository) Create(ctx context.Context, input Input) (Record, error) {
	return r.create(ctx, input, "atk_"+postgres.RandomHex(24), "rtk_"+postgres.RandomHex(24))
}

func (r PostgresRepository) CreateFixedAccess(ctx context.Context, input Input, accessToken string) (Record, error) {
	return r.create(ctx, input, accessToken, "rtk_"+postgres.RandomHex(24))
}

func (r PostgresRepository) create(ctx context.Context, input Input, accessToken string, refreshToken string) (Record, error) {
	record := Record{
		SessionID:     postgres.NewID("session"),
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		AuthAccountID: input.AuthAccountID,
		UserID:        input.UserID,
		AuthMethods:   append([]string(nil), input.AuthMethods...),
	}
	_, err := r.exec.ExecContext(ctx, `
		INSERT INTO auth_sessions (session_id, auth_account_id, user_id, access_token, refresh_token_hash, auth_methods, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')`,
		record.SessionID, record.AuthAccountID, record.UserID, record.AccessToken, postgres.HashToken(record.RefreshToken), strings.Join(record.AuthMethods, ","))
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func (r PostgresRepository) FindByAccessToken(ctx context.Context, token string) (Record, error) {
	row := r.exec.QueryRowContext(ctx, `
		SELECT session_id, access_token, auth_account_id, user_id, auth_methods
		FROM auth_sessions
		WHERE access_token = $1 AND status = 'active'`, token)
	var record Record
	var methods string
	if err := row.Scan(&record.SessionID, &record.AccessToken, &record.AuthAccountID, &record.UserID, &methods); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, autherror.ErrSessionNotFound
		}
		return Record{}, err
	}
	record.AuthMethods = postgres.SplitCSV(methods)
	return record, nil
}

func (r PostgresRepository) Refresh(ctx context.Context, refreshToken string) (Rotation, error) {
	newAccessToken := "atk_" + postgres.RandomHex(24)
	newRefreshToken := "rtk_" + postgres.RandomHex(24)
	row := r.exec.QueryRowContext(ctx, `
		WITH current AS (
			SELECT session_id, access_token, auth_account_id, user_id, auth_methods
			FROM auth_sessions
			WHERE refresh_token_hash = $1 AND status = 'active'
			FOR UPDATE
		),
		updated AS (
			UPDATE auth_sessions s
			SET access_token = $2, refresh_token_hash = $3, rotated_at = now()
			FROM current
			WHERE s.session_id = current.session_id
			RETURNING current.access_token AS previous_access_token, s.session_id, s.access_token, s.auth_account_id, s.user_id, s.auth_methods
		)
		SELECT previous_access_token, session_id, access_token, auth_account_id, user_id, auth_methods
		FROM updated`,
		postgres.HashToken(refreshToken), newAccessToken, postgres.HashToken(newRefreshToken))
	var previousAccessToken string
	var record Record
	var methods string
	if err := row.Scan(&previousAccessToken, &record.SessionID, &record.AccessToken, &record.AuthAccountID, &record.UserID, &methods); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Rotation{}, autherror.ErrSessionNotFound
		}
		return Rotation{}, err
	}
	record.RefreshToken = newRefreshToken
	record.AuthMethods = postgres.SplitCSV(methods)
	return Rotation{PreviousAccessToken: previousAccessToken, Session: record}, nil
}

func (r PostgresRepository) RevokeBySessionID(ctx context.Context, sessionID string) (Record, error) {
	row := r.exec.QueryRowContext(ctx, `
		UPDATE auth_sessions
		SET status = 'revoked', revoked_at = now()
		WHERE session_id = $1 AND status = 'active'
		RETURNING session_id, access_token, auth_account_id, user_id, auth_methods`, sessionID)
	var record Record
	var methods string
	if err := row.Scan(&record.SessionID, &record.AccessToken, &record.AuthAccountID, &record.UserID, &methods); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, autherror.ErrSessionNotFound
		}
		return Record{}, err
	}
	record.AuthMethods = postgres.SplitCSV(methods)
	return record, nil
}

func (r PostgresRepository) RevokeByAccessToken(ctx context.Context, token string) (Record, error) {
	record, err := r.FindByAccessToken(ctx, token)
	if err != nil {
		return Record{}, err
	}
	if _, err := r.RevokeBySessionID(ctx, record.SessionID); err != nil {
		return Record{}, err
	}
	return record, nil
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_sessions (
		session_id TEXT PRIMARY KEY,
		auth_account_id TEXT NOT NULL REFERENCES auth_accounts(auth_account_id),
		user_id TEXT NOT NULL,
		access_token TEXT NOT NULL UNIQUE,
		refresh_token_hash TEXT NOT NULL,
		auth_methods TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		rotated_at TIMESTAMPTZ,
		revoked_at TIMESTAMPTZ
	)`,
	`ALTER TABLE auth_sessions DROP COLUMN IF EXISTS refresh_token`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS refresh_token_hash TEXT`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ`,
	`CREATE UNIQUE INDEX IF NOT EXISTS auth_sessions_refresh_token_hash_active_uq ON auth_sessions(refresh_token_hash) WHERE refresh_token_hash IS NOT NULL AND status = 'active'`,
}
