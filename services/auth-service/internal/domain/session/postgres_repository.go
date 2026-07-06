package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

func NewPostgresRepository(pool *pgxpool.Pool) PostgresRepository {
	return PostgresRepository{pool: pool}
}

func NewPostgresTxRepository(tx pgx.Tx) PostgresRepository {
	return PostgresRepository{tx: tx}
}

func (r PostgresRepository) Create(ctx context.Context, input Input) (Record, error) {
	return r.create(ctx, input, NewRefreshToken())
}

func (r PostgresRepository) create(ctx context.Context, input Input, refreshToken string) (Record, error) {
	record := Record{
		SessionID:        newID("session"),
		AccessJTI:        strings.TrimSpace(input.AccessJTI),
		RefreshToken:     refreshToken,
		AuthAccountID:    input.AuthAccountID,
		UserID:           input.UserID,
		Email:            strings.TrimSpace(input.Email),
		AccessExpiresAt:  input.AccessExpiresAt,
		RefreshExpiresAt: input.RefreshExpiresAt,
		AuthMethods:      append([]string(nil), input.AuthMethods...),
	}
	_, err := r.exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, auth_account_id, user_id, email, access_token, access_jti,
			access_expires_at, refresh_token_hash, refresh_expires_at, auth_methods, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'active')`,
		record.SessionID, record.AuthAccountID, record.UserID, record.Email, record.AccessJTI, record.AccessJTI,
		record.AccessExpiresAt, hashToken(record.RefreshToken), record.RefreshExpiresAt, strings.Join(record.AuthMethods, ","))
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func (r PostgresRepository) FindByAccessJTI(ctx context.Context, jti string) (Record, error) {
	row := r.queryRow(ctx, `
		SELECT s.session_id, s.access_jti, s.auth_account_id, s.user_id, s.email, s.access_expires_at, s.refresh_expires_at, s.auth_methods
		FROM auth_sessions s
		JOIN auth_accounts a ON a.auth_account_id = s.auth_account_id
		WHERE s.access_jti = $1
			AND s.status = 'active'
			AND a.status = 'active'
			AND s.access_expires_at > now()
			AND s.refresh_expires_at > now()`, jti)
	var record Record
	var methods string
	if err := row.Scan(&record.SessionID, &record.AccessJTI, &record.AuthAccountID, &record.UserID, &record.Email, &record.AccessExpiresAt, &record.RefreshExpiresAt, &methods); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, ErrTokenRevoked.New("session not found")
		}
		return Record{}, err
	}
	record.AuthMethods = splitCSV(methods)
	return record, nil
}

func (r PostgresRepository) Refresh(ctx context.Context, refreshToken string, input Input) (Rotation, error) {
	newRefreshToken := NewRefreshToken()
	row := r.queryRow(ctx, `
		WITH current AS (
			SELECT s.session_id, s.access_jti, s.auth_account_id, s.user_id, s.email, s.auth_methods
			FROM auth_sessions s
			JOIN auth_accounts a ON a.auth_account_id = s.auth_account_id
			WHERE s.refresh_token_hash = $1
				AND s.status = 'active'
				AND a.status = 'active'
				AND s.refresh_expires_at > now()
			FOR UPDATE
		),
		updated AS (
			UPDATE auth_sessions s
			SET access_token = $2,
				access_jti = $2,
				access_expires_at = $3,
				refresh_token_hash = $4,
				refresh_expires_at = $5,
				rotated_at = now()
			FROM current
			WHERE s.session_id = current.session_id
			RETURNING current.access_jti AS previous_access_jti, s.session_id, s.access_jti, s.auth_account_id, s.user_id, s.email, s.access_expires_at, s.refresh_expires_at, s.auth_methods
		)
		SELECT previous_access_jti, session_id, access_jti, auth_account_id, user_id, email, access_expires_at, refresh_expires_at, auth_methods
		FROM updated`,
		hashToken(refreshToken), input.AccessJTI, input.AccessExpiresAt, hashToken(newRefreshToken), input.RefreshExpiresAt)
	var previousAccessToken string
	var record Record
	var methods string
	if err := row.Scan(&previousAccessToken, &record.SessionID, &record.AccessJTI, &record.AuthAccountID, &record.UserID, &record.Email, &record.AccessExpiresAt, &record.RefreshExpiresAt, &methods); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Rotation{}, ErrInvalidRefreshToken.New("refresh token not found")
		}
		return Rotation{}, err
	}
	record.RefreshToken = newRefreshToken
	record.AuthMethods = splitCSV(methods)
	return Rotation{PreviousAccessToken: previousAccessToken, Session: record}, nil
}

func (r PostgresRepository) RevokeBySessionID(ctx context.Context, sessionID string) (Record, error) {
	row := r.queryRow(ctx, `
		UPDATE auth_sessions
		SET status = 'revoked', revoked_at = now()
		WHERE session_id = $1 AND status = 'active'
		RETURNING session_id, access_jti, auth_account_id, user_id, email, access_expires_at, refresh_expires_at, auth_methods`, sessionID)
	var record Record
	var methods string
	if err := row.Scan(&record.SessionID, &record.AccessJTI, &record.AuthAccountID, &record.UserID, &record.Email, &record.AccessExpiresAt, &record.RefreshExpiresAt, &methods); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Record{}, ErrTokenRevoked.New("session not found")
		}
		return Record{}, err
	}
	record.AuthMethods = splitCSV(methods)
	return record, nil
}

func (r PostgresRepository) RevokeByAccessJTI(ctx context.Context, jti string) (Record, error) {
	record, err := r.FindByAccessJTI(ctx, jti)
	if err != nil {
		return Record{}, err
	}
	if _, err := r.RevokeBySessionID(ctx, record.SessionID); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (r PostgresRepository) exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if r.tx != nil {
		return r.tx.Exec(ctx, sql, args...)
	}
	return r.pool.Exec(ctx, sql, args...)
}

func (r PostgresRepository) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if r.tx != nil {
		return r.tx.QueryRow(ctx, sql, args...)
	}
	return r.pool.QueryRow(ctx, sql, args...)
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, randomHex(12))
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto random failed: %v", err))
	}
	return hex.EncodeToString(buf)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS email TEXT`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS access_jti TEXT`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS access_expires_at TIMESTAMPTZ`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS refresh_expires_at TIMESTAMPTZ`,
	`UPDATE auth_sessions SET access_jti = access_token WHERE access_jti IS NULL`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ`,
	`ALTER TABLE auth_sessions ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ`,
	`CREATE UNIQUE INDEX IF NOT EXISTS auth_sessions_refresh_token_hash_active_uq ON auth_sessions(refresh_token_hash) WHERE refresh_token_hash IS NOT NULL AND status = 'active'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS auth_sessions_access_jti_active_uq ON auth_sessions(access_jti) WHERE access_jti IS NOT NULL AND status = 'active'`,
}
