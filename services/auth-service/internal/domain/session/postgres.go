package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("session not found")

type PostgresRepository struct {
	pool   *pgxpool.Pool
	status *StatusService
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, tx pgx.Tx, params CreateParams) error {
	s := params.Session
	c := params.Credential
	idleExpiry := any(nil)
	if s.Channel == ChannelWeb {
		idleExpiry = s.ExpiresAt
	}
	csrfKeyVersion := any(nil)
	if c.Type == "web_refresh_cookie" {
		csrfKeyVersion = int16(1)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, identity_link_id, authentication_method,
			session_status, client_channel, remember_me, issued_at, idle_expires_at, absolute_expires_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, now(), $8, $9, now(), now())
	`, s.ID, s.UserID, s.IdentityID, s.IdentityLink, s.Method, s.Channel, s.RememberMe, idleExpiry, s.ExpiresAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_session_credentials (
			session_credential_id, session_id, credential_type, credential_status, secret_hash,
			secret_key_version, csrf_key_version, csrf_token_hash, refresh_family_id, issued_at, expires_at, created_at
		) VALUES ($1, $2, $3, 'active', $4, 1, $5, $6, $7, now(), $8, now())
	`, c.ID, s.ID, c.Type, c.SecretHash, csrfKeyVersion, nullableBytes(c.CSRFHash), c.FamilyID, c.ExpiresAt)
	return err
}

func (r *PostgresRepository) FindByWebSecret(ctx context.Context, secretHash []byte) (Session, Credential, error) {
	return r.find(ctx, r.pool, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type = 'web_refresh_cookie' AND c.credential_status = 'active'
			AND c.secret_hash = $1 AND s.session_status = 'active' AND s.absolute_expires_at > now()
	`, secretHash)
}

func (r *PostgresRepository) FindByWebSecretForUpdate(ctx context.Context, tx pgx.Tx, secretHash []byte) (Session, Credential, error) {
	return r.find(ctx, tx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type = 'web_refresh_cookie' AND c.secret_hash = $1
		FOR UPDATE
	`, secretHash)
}

func (r *PostgresRepository) FindByRefreshSecretForUpdate(ctx context.Context, tx pgx.Tx, secretHash []byte) (Session, Credential, error) {
	return r.find(ctx, tx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type IN ('web_refresh_cookie', 'mobile_refresh_token') AND c.secret_hash = $1
		FOR UPDATE
	`, secretHash)
}

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *PostgresRepository) find(ctx context.Context, q rowQueryer, sql string, secretHash []byte) (Session, Credential, error) {
	var s Session
	var c Credential
	err := q.QueryRow(ctx, sql, secretHash).Scan(
		&s.ID, &s.UserID, &s.IdentityID, &s.IdentityLink, &s.Method,
		&s.Channel, &s.RememberMe,
		&s.AuthenticatedAt, &s.ExpiresAt, &s.Status,
		&c.ID, &c.SessionID, &c.Type, &c.Status, &c.SecretHash, &c.CSRFHash, &c.FamilyID, &c.ExpiresAt, &c.DeliveryRecoveryExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, Credential{}, ErrNotFound
	}
	return s, c, err
}

func (r *PostgresRepository) FindRecoveryWebSecretForUpdate(ctx context.Context, tx pgx.Tx, secretHash []byte) (Session, Credential, error) {
	return r.find(ctx, tx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type = 'web_refresh_cookie' AND c.credential_status = 'rotated_pending_delivery'
			AND c.secret_hash = $1 AND s.session_status = 'active' AND s.absolute_expires_at > now()
		FOR UPDATE
	`, secretHash)
}

func (r *PostgresRepository) FindActiveForUpdate(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (Session, error) {
	return findActiveSession(ctx, tx, sessionID, true)
}

func (r *PostgresRepository) FindActiveCredentialForUpdate(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, credentialType string) (Credential, error) {
	var credential Credential
	err := tx.QueryRow(ctx, `
		SELECT session_credential_id, session_id, credential_type, credential_status,
			secret_hash, csrf_token_hash, refresh_family_id, expires_at, delivery_recovery_expires_at
		FROM auth_session_credentials
		WHERE session_id = $1 AND credential_type = $2 AND credential_status = 'active'
		FOR UPDATE
	`, sessionID, credentialType).Scan(
		&credential.ID, &credential.SessionID, &credential.Type, &credential.Status,
		&credential.SecretHash, &credential.CSRFHash, &credential.FamilyID, &credential.ExpiresAt, &credential.DeliveryRecoveryExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	return credential, err
}

func (r *PostgresRepository) RotateRefresh(ctx context.Context, tx pgx.Tx, previous, next Credential) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'rotated', rotated_at = now(), rotated_to_credential_id = $2,
			row_version = row_version + 1
		WHERE session_credential_id = $1 AND credential_status = 'active'
	`, previous.ID, next.ID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_session_credentials (
			session_credential_id, session_id, credential_type, credential_status, secret_hash,
			secret_key_version, csrf_key_version, csrf_token_hash, refresh_family_id,
			rotated_from_credential_id, issued_at, expires_at, created_at
		) VALUES ($1, $2, $3, 'active', $4, 1, $5, $6, $7, $8, now(), $9, now())
	`, next.ID, next.SessionID, next.Type, next.SecretHash, csrfVersion(next), nullableBytes(next.CSRFHash), next.FamilyID, previous.ID, next.ExpiresAt)
	return err
}

// RotateForDelivery makes the prior credential recovery-only. It deliberately
// never leaves the old credential active, so ordinary authentication cannot
// use a credential that is retained only to recover a lost response.
func (r *PostgresRepository) RotateForDelivery(ctx context.Context, tx pgx.Tx, previous, next Credential, recoveryExpiresAt time.Time) error {
	result, err := tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'rotated_pending_delivery', rotated_at = now(),
			rotated_to_credential_id = $2, delivery_recovery_expires_at = $3,
			row_version = row_version + 1
		WHERE session_credential_id = $1 AND credential_status = 'active'
	`, previous.ID, next.ID, recoveryExpiresAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	csrfKeyVersion := any(nil)
	if next.Type == "web_refresh_cookie" {
		csrfKeyVersion = int16(1)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_session_credentials (
			session_credential_id, session_id, credential_type, credential_status, secret_hash,
			secret_key_version, csrf_key_version, csrf_token_hash, refresh_family_id, rotated_from_credential_id,
			issued_at, expires_at, created_at
		) VALUES ($1, $2, $3, 'active', $4, 1, $5, $6, $7, $8, now(), $9, now())
	`, next.ID, next.SessionID, next.Type, next.SecretHash, csrfKeyVersion, nullableBytes(next.CSRFHash), next.FamilyID, previous.ID, next.ExpiresAt)
	return err
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func csrfVersion(credential Credential) any {
	if credential.Type == "web_refresh_cookie" {
		return int16(1)
	}
	return nil
}

func (r *PostgresRepository) Rebind(ctx context.Context, tx pgx.Tx, session Session) error {
	result, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET identity_id = $2, identity_link_id = $3, authentication_method = $4,
			last_authenticated_at = now(), updated_at = now(), row_version = row_version + 1
		WHERE session_id = $1 AND session_status = 'active'
	`, session.ID, session.IdentityID, session.IdentityLink, session.Method)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) Revoke(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $2, updated_at = now()
		WHERE session_id = $1 AND session_status = 'active'
	`, sessionID, reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'revoked', revoked_at = now()
		WHERE session_id = $1 AND credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, sessionID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_reauth_proofs
		SET invalidated_at = now()
		WHERE session_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL
	`, sessionID)
	return err
}

func (r *PostgresRepository) RevokeForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $2, updated_at = now()
		WHERE user_id = $1 AND session_status = 'active'
	`, userID, reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_session_credentials c
		SET credential_status = 'revoked', revoked_at = now()
		FROM auth_sessions s
		WHERE c.session_id = s.session_id AND s.user_id = $1 AND c.credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_reauth_proofs p
		SET invalidated_at = now()
		FROM auth_sessions s
		WHERE p.session_id = s.session_id AND s.user_id = $1 AND p.consumed_at IS NULL AND p.invalidated_at IS NULL
	`, userID)
	return err
}

func (r *PostgresRepository) RevokeForIdentityLinkExcept(ctx context.Context, tx pgx.Tx, identityLinkID, keepSessionID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $3, updated_at = now()
		WHERE identity_link_id = $1 AND session_id <> $2 AND session_status = 'active'
	`, identityLinkID, keepSessionID, reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_session_credentials c
		SET credential_status = 'revoked', revoked_at = now()
		FROM auth_sessions s
		WHERE c.session_id = s.session_id AND s.identity_link_id = $1 AND s.session_id <> $2
			AND c.credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, identityLinkID, keepSessionID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_reauth_proofs p
		SET invalidated_at = now()
		FROM auth_sessions s
		WHERE p.session_id = s.session_id AND s.identity_link_id = $1 AND s.session_id <> $2
			AND p.consumed_at IS NULL AND p.invalidated_at IS NULL
	`, identityLinkID, keepSessionID)
	return err
}

func (r *PostgresRepository) MarkReuseDetected(ctx context.Context, tx pgx.Tx, sessionID, familyID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'reuse_detected', reuse_detected_at = now(), revoked_at = now()
		WHERE refresh_family_id = $1 AND credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, familyID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'reuse_detected', reuse_detected_at = now(), revocation_reason = 'refresh_reuse', updated_at = now()
		WHERE session_id = $1
	`, sessionID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_reauth_proofs
		SET invalidated_at = now()
		WHERE session_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL
	`, sessionID)
	return err
}

func (r *PostgresRepository) FindActive(ctx context.Context, sessionID uuid.UUID) (Session, error) {
	return findActiveSession(ctx, r.pool, sessionID, false)
}

func findActiveSession(ctx context.Context, q rowQueryer, sessionID uuid.UUID, forUpdate bool) (Session, error) {
	var s Session
	query := `
		SELECT session_id, user_id, identity_id, identity_link_id, authentication_method,
			client_channel, remember_me,
			last_authenticated_at, absolute_expires_at, session_status
		FROM auth_sessions
		WHERE session_id = $1 AND session_status = 'active' AND absolute_expires_at > now()
	`
	if forUpdate {
		query += " FOR UPDATE"
	}
	err := q.QueryRow(ctx, query, sessionID).Scan(
		&s.ID, &s.UserID, &s.IdentityID, &s.IdentityLink, &s.Method,
		&s.Channel, &s.RememberMe,
		&s.AuthenticatedAt, &s.ExpiresAt, &s.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}
