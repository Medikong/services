package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/config"
	"github.com/Medikong/services/services/auth-service/internal/model"
	"github.com/Medikong/services/services/auth-service/internal/repository"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := database.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	return database.RunMigrations(ctx, s.db, migrations)
}

func (s *Store) CreateEmailAccount(ctx context.Context, email string, passwordHash string) (model.AccountCredential, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "db.auth.create_email_account", attribute.String("db.system", "postgresql"))
	defer span.End()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AccountCredential{}, err
	}
	defer func() { _ = tx.Rollback() }()

	authAccountID := newID("auth")
	userID := newID("user")
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_accounts (auth_account_id) VALUES ($1)`, authAccountID); err != nil {
		return model.AccountCredential{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_credentials (auth_account_id, email, password_hash) VALUES ($1, $2, $3)`, authAccountID, email, passwordHash); err != nil {
		if isUniqueViolation(err) {
			return model.AccountCredential{}, repository.ErrAlreadyExists
		}
		return model.AccountCredential{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_user_links (auth_account_id, user_id) VALUES ($1, $2)`, authAccountID, userID); err != nil {
		return model.AccountCredential{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO role_grants (auth_account_id, role) VALUES ($1, $2)`, authAccountID, string(rbac.RoleCustomer)); err != nil {
		return model.AccountCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.AccountCredential{}, err
	}
	return model.AccountCredential{
		AuthAccountID: authAccountID,
		UserID:        userID,
		Email:         email,
		PasswordHash:  passwordHash,
		Roles:         []string{string(rbac.RoleCustomer)},
	}, nil
}

func (s *Store) FindByEmail(ctx context.Context, email string) (model.AccountCredential, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.auth_account_id, l.user_id, c.email, c.password_hash, COALESCE(r.role, '')
		FROM auth_credentials c
		JOIN auth_accounts a ON a.auth_account_id = c.auth_account_id
		JOIN auth_user_links l ON l.auth_account_id = a.auth_account_id
		LEFT JOIN role_grants r ON r.auth_account_id = a.auth_account_id
		WHERE c.email = $1
		ORDER BY r.role`, email)
	if err != nil {
		return model.AccountCredential{}, err
	}
	defer rows.Close()
	account := model.AccountCredential{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&account.AuthAccountID, &account.UserID, &account.Email, &account.PasswordHash, &role); err != nil {
			return model.AccountCredential{}, err
		}
		if role != "" {
			account.Roles = append(account.Roles, role)
		}
	}
	if err := rows.Err(); err != nil {
		return model.AccountCredential{}, err
	}
	if account.AuthAccountID == "" {
		return model.AccountCredential{}, repository.ErrInvalidCredentials
	}
	return account, nil
}

func (s *Store) CreateSession(ctx context.Context, input repository.SessionInput) (repository.Session, error) {
	session := repository.Session{
		SessionID:    newID("session"),
		AccessToken:  "atk_" + randomHex(24),
		RefreshToken: "rtk_" + randomHex(24),
		Principal: model.AccountCredential{
			AuthAccountID: input.AuthAccountID,
			UserID:        input.UserID,
			Roles:         append([]string(nil), input.Roles...),
		},
		AuthMethods: append([]string(nil), input.AuthMethods...),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_sessions (session_id, auth_account_id, user_id, access_token, refresh_token_hash, auth_methods, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')`,
		session.SessionID, input.AuthAccountID, input.UserID, session.AccessToken, hashToken(session.RefreshToken), strings.Join(input.AuthMethods, ","))
	if err != nil {
		return repository.Session{}, err
	}
	return session, nil
}

func (s *Store) FindSessionByAccessToken(ctx context.Context, token string) (repository.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT s.session_id, s.access_token, l.auth_account_id, s.user_id, s.auth_methods
		FROM auth_sessions s
		JOIN auth_user_links l ON l.user_id = s.user_id
		WHERE s.access_token = $1 AND s.status = 'active'`, token)
	var session repository.Session
	var methods string
	if err := row.Scan(&session.SessionID, &session.AccessToken, &session.Principal.AuthAccountID, &session.Principal.UserID, &methods); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repository.Session{}, repository.ErrSessionNotFound
		}
		return repository.Session{}, err
	}
	session.AuthMethods = splitCSV(methods)
	roles, err := s.rolesForAccount(ctx, session.Principal.AuthAccountID)
	if err != nil {
		return repository.Session{}, err
	}
	session.Principal.Roles = roles
	return session, nil
}

func (s *Store) RefreshSession(ctx context.Context, refreshToken string) (repository.SessionRotation, error) {
	newAccessToken := "atk_" + randomHex(24)
	newRefreshToken := "rtk_" + randomHex(24)
	row := s.db.QueryRowContext(ctx, `
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
		hashToken(refreshToken), newAccessToken, hashToken(newRefreshToken))
	var previousAccessToken string
	var rotated repository.Session
	var methods string
	if err := row.Scan(&previousAccessToken, &rotated.SessionID, &rotated.AccessToken, &rotated.Principal.AuthAccountID, &rotated.Principal.UserID, &methods); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repository.SessionRotation{}, repository.ErrSessionNotFound
		}
		return repository.SessionRotation{}, err
	}
	rotated.RefreshToken = newRefreshToken
	rotated.AuthMethods = splitCSV(methods)
	roles, err := s.rolesForAccount(ctx, rotated.Principal.AuthAccountID)
	if err != nil {
		return repository.SessionRotation{}, err
	}
	rotated.Principal.Roles = roles
	return repository.SessionRotation{PreviousAccessToken: previousAccessToken, Session: rotated}, nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID string) (repository.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE auth_sessions
		SET status = 'revoked', revoked_at = now()
		WHERE session_id = $1 AND status = 'active'
		RETURNING session_id, access_token, auth_account_id, user_id, auth_methods`, sessionID)
	var session repository.Session
	var methods string
	if err := row.Scan(&session.SessionID, &session.AccessToken, &session.Principal.AuthAccountID, &session.Principal.UserID, &methods); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repository.Session{}, repository.ErrSessionNotFound
		}
		return repository.Session{}, err
	}
	session.AuthMethods = splitCSV(methods)
	roles, err := s.rolesForAccount(ctx, session.Principal.AuthAccountID)
	if err != nil {
		return repository.Session{}, err
	}
	session.Principal.Roles = roles
	return session, nil
}

func (s *Store) RevokeByAccessToken(ctx context.Context, token string) (repository.Session, error) {
	session, err := s.FindSessionByAccessToken(ctx, token)
	if err != nil {
		return repository.Session{}, err
	}
	if _, err := s.RevokeSession(ctx, session.SessionID); err != nil {
		return repository.Session{}, err
	}
	return session, nil
}

func (s *Store) IssueTestToken(ctx context.Context, token string, userID string, roles []string) (repository.Session, error) {
	ctx, span := telemetry.StartSpan(ctx, config.ServiceName, "db.auth.issue_test_token", attribute.String("db.system", "postgresql"))
	defer span.End()

	if userID == "" {
		userID = newID("test-user")
	}
	authAccountID := "test-auth-" + userID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return repository.Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_accounts (auth_account_id) VALUES ($1) ON CONFLICT DO NOTHING`, authAccountID); err != nil {
		return repository.Session{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_user_links (auth_account_id, user_id) VALUES ($1, $2) ON CONFLICT (auth_account_id) DO UPDATE SET user_id = EXCLUDED.user_id`, authAccountID, userID); err != nil {
		return repository.Session{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_grants WHERE auth_account_id = $1`, authAccountID); err != nil {
		return repository.Session{}, err
	}
	for _, role := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO role_grants (auth_account_id, role) VALUES ($1, $2) ON CONFLICT DO NOTHING`, authAccountID, role); err != nil {
			return repository.Session{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE access_token = $1`, token); err != nil {
		return repository.Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return repository.Session{}, err
	}
	return s.createFixedAccessSession(ctx, authAccountID, userID, roles, token)
}

func (s *Store) createFixedAccessSession(ctx context.Context, authAccountID string, userID string, roles []string, accessToken string) (repository.Session, error) {
	session := repository.Session{
		SessionID:    newID("session"),
		AccessToken:  accessToken,
		RefreshToken: "rtk_" + randomHex(24),
		Principal: model.AccountCredential{
			AuthAccountID: authAccountID,
			UserID:        userID,
			Roles:         append([]string(nil), roles...),
		},
		AuthMethods: []string{"test_token"},
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_sessions (session_id, auth_account_id, user_id, access_token, refresh_token_hash, auth_methods, status)
		VALUES ($1, $2, $3, $4, $5, 'test_token', 'active')`,
		session.SessionID, authAccountID, userID, session.AccessToken, hashToken(session.RefreshToken))
	return session, err
}

func (s *Store) rolesForAccount(ctx context.Context, authAccountID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role FROM role_grants WHERE auth_account_id = $1 ORDER BY role`, authAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roles, nil
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_accounts (
		auth_account_id TEXT PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS auth_credentials (
		credential_id BIGSERIAL PRIMARY KEY,
		auth_account_id TEXT NOT NULL REFERENCES auth_accounts(auth_account_id),
		email TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS auth_user_links (
		auth_user_link_id BIGSERIAL PRIMARY KEY,
		auth_account_id TEXT NOT NULL UNIQUE REFERENCES auth_accounts(auth_account_id),
		user_id TEXT NOT NULL UNIQUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`ALTER TABLE auth_user_links DROP COLUMN IF EXISTS real_name`,
	`CREATE TABLE IF NOT EXISTS role_grants (
		auth_account_id TEXT NOT NULL REFERENCES auth_accounts(auth_account_id),
		role TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (auth_account_id, role)
	)`,
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
