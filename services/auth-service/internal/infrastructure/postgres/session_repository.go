package postgres

import (
	"context"
	"errors"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionRepository serves non-transactional domain reads.
type SessionRepository struct {
	pool *pgxpool.Pool
}

const selectSessionStatus = `
	SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
		s.client_channel, s.remember_me, s.last_authenticated_at, s.absolute_expires_at,
		CASE WHEN u.status = 'active' THEN s.session_status ELSE 'revoked' END, s.row_version
	FROM auth_sessions s
	JOIN auth_user_auth_states u ON u.user_id = s.user_id
	WHERE s.session_id = $1
`

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

func (r *SessionRepository) FindByWebSecret(ctx context.Context, secretHash []byte) (domainsession.Session, domainsession.Credential, error) {
	return scanSessionCredential(r.pool.QueryRow(ctx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status, s.row_version,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type = 'web_refresh_cookie' AND c.credential_status = 'active'
			AND c.secret_hash = $1 AND s.session_status = 'active' AND s.absolute_expires_at > now()
	`, secretHash))
}

func (r *SessionRepository) FindActive(ctx context.Context, sessionID uuid.UUID) (domainsession.Session, error) {
	return scanSession(r.pool.QueryRow(ctx, `
		SELECT session_id, user_id, identity_id, identity_link_id, authentication_method,
			client_channel, remember_me, last_authenticated_at, absolute_expires_at, session_status, row_version
		FROM auth_sessions
		WHERE session_id = $1 AND session_status = 'active' AND absolute_expires_at > now()
	`, sessionID))
}

func (r *SessionRepository) FindStatus(ctx context.Context, sessionID uuid.UUID) (domainsession.Session, error) {
	return scanSession(r.pool.QueryRow(ctx, selectSessionStatus, sessionID))
}

func (r *SessionRepository) FindStatusForReconciliation(ctx context.Context, sessionID uuid.UUID) (domainsession.Session, error) {
	return scanSession(r.pool.QueryRow(ctx, selectSessionStatus+`FOR SHARE OF s`, sessionID))
}

type SessionTxRepository struct {
	tx pgx.Tx
}

func NewSessionTxRepository(tx pgx.Tx) *SessionTxRepository {
	return &SessionTxRepository{tx: tx}
}

func (r *SessionTxRepository) Create(ctx context.Context, params domainsession.CreateParams) error {
	current := params.Session
	credential := params.Credential
	var idleExpiry *time.Time
	if current.Channel == domainsession.ChannelWeb {
		idleExpiry = &current.ExpiresAt
	}
	var csrfKeyVersion *int16
	if credential.Type == "web_refresh_cookie" {
		version := int16(1)
		csrfKeyVersion = &version
	}
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, identity_link_id, authentication_method,
			session_status, client_channel, remember_me, issued_at, idle_expires_at, absolute_expires_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, now(), $8, $9, now(), now())
	`, current.ID, current.UserID, current.IdentityID, current.IdentityLink, current.Method, current.Channel, current.RememberMe, idleExpiry, current.ExpiresAt)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		INSERT INTO auth_session_credentials (
			session_credential_id, session_id, credential_type, credential_status, secret_hash,
			secret_key_version, csrf_key_version, csrf_token_hash, refresh_family_id, issued_at, expires_at, created_at
		) VALUES ($1, $2, $3, 'active', $4, 1, $5, $6, $7, now(), $8, now())
	`, credential.ID, current.ID, credential.Type, credential.SecretHash, csrfKeyVersion, nullableSessionBytes(credential.CSRFHash), credential.FamilyID, credential.ExpiresAt)
	return err
}

func (r *SessionTxRepository) FindByWebSecretForUpdate(ctx context.Context, secretHash []byte) (domainsession.Session, domainsession.Credential, error) {
	return scanSessionCredential(r.tx.QueryRow(ctx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status, s.row_version,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type = 'web_refresh_cookie' AND c.secret_hash = $1
		FOR UPDATE
	`, secretHash))
}

func (r *SessionTxRepository) FindByRefreshSecretForUpdate(ctx context.Context, secretHash []byte) (domainsession.Session, domainsession.Credential, error) {
	return scanSessionCredential(r.tx.QueryRow(ctx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status, s.row_version,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type IN ('web_refresh_cookie', 'mobile_refresh_token') AND c.secret_hash = $1
		FOR UPDATE
	`, secretHash))
}

func (r *SessionTxRepository) FindRecoveryWebSecretForUpdate(ctx context.Context, secretHash []byte) (domainsession.Session, domainsession.Credential, error) {
	return scanSessionCredential(r.tx.QueryRow(ctx, `
		SELECT s.session_id, s.user_id, s.identity_id, s.identity_link_id, s.authentication_method,
			s.client_channel, s.remember_me,
			s.last_authenticated_at, s.absolute_expires_at, s.session_status, s.row_version,
			c.session_credential_id, c.session_id, c.credential_type, c.credential_status,
			c.secret_hash, c.csrf_token_hash, c.refresh_family_id, c.expires_at, c.delivery_recovery_expires_at
		FROM auth_session_credentials c JOIN auth_sessions s ON s.session_id = c.session_id
		WHERE c.credential_type = 'web_refresh_cookie' AND c.credential_status = 'rotated_pending_delivery'
			AND c.secret_hash = $1 AND s.session_status = 'active' AND s.absolute_expires_at > now()
		FOR UPDATE
	`, secretHash))
}

func (r *SessionTxRepository) FindActiveForUpdate(ctx context.Context, sessionID uuid.UUID) (domainsession.Session, error) {
	return scanSession(r.tx.QueryRow(ctx, `
		SELECT session_id, user_id, identity_id, identity_link_id, authentication_method,
			client_channel, remember_me, last_authenticated_at, absolute_expires_at, session_status, row_version
		FROM auth_sessions
		WHERE session_id = $1 AND session_status = 'active' AND absolute_expires_at > now()
		FOR UPDATE
	`, sessionID))
}

func (r *SessionTxRepository) FindActiveForUserForUpdate(ctx context.Context, userID uuid.UUID) ([]domainsession.Session, error) {
	rows, err := r.tx.Query(ctx, `
		SELECT session_id, user_id, identity_id, identity_link_id, authentication_method,
			client_channel, remember_me, last_authenticated_at, absolute_expires_at, session_status, row_version
		FROM auth_sessions
		WHERE user_id = $1 AND session_status = 'active' AND absolute_expires_at > now()
		ORDER BY session_id
		FOR UPDATE
	`, userID)
	if err != nil {
		return nil, err
	}
	return scanSessions(rows)
}

func (r *SessionTxRepository) FindActiveForIdentityLinkExceptForUpdate(ctx context.Context, identityLinkID, keepSessionID uuid.UUID) ([]domainsession.Session, error) {
	rows, err := r.tx.Query(ctx, `
		SELECT session_id, user_id, identity_id, identity_link_id, authentication_method,
			client_channel, remember_me, last_authenticated_at, absolute_expires_at, session_status, row_version
		FROM auth_sessions
		WHERE identity_link_id = $1 AND session_id <> $2
		  AND session_status = 'active' AND absolute_expires_at > now()
		ORDER BY session_id
		FOR UPDATE
	`, identityLinkID, keepSessionID)
	if err != nil {
		return nil, err
	}
	return scanSessions(rows)
}

func (r *SessionTxRepository) FindActiveCredentialForUpdate(ctx context.Context, sessionID uuid.UUID, credentialType string) (domainsession.Credential, error) {
	var credential domainsession.Credential
	err := r.tx.QueryRow(ctx, `
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
		return domainsession.Credential{}, domainsession.ErrNotFound
	}
	return credential, err
}

func (r *SessionTxRepository) RotateRefresh(ctx context.Context, previous, next domainsession.Credential) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'rotated', rotated_at = now(), rotated_to_credential_id = $2,
			row_version = row_version + 1
		WHERE session_credential_id = $1 AND credential_status = 'active'
	`, previous.ID, next.ID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		INSERT INTO auth_session_credentials (
			session_credential_id, session_id, credential_type, credential_status, secret_hash,
			secret_key_version, csrf_key_version, csrf_token_hash, refresh_family_id,
			rotated_from_credential_id, issued_at, expires_at, created_at
		) VALUES ($1, $2, $3, 'active', $4, 1, $5, $6, $7, $8, now(), $9, now())
	`, next.ID, next.SessionID, next.Type, next.SecretHash, sessionCSRFVersion(next), nullableSessionBytes(next.CSRFHash), next.FamilyID, previous.ID, next.ExpiresAt)
	return err
}

func (r *SessionTxRepository) RotateForDelivery(ctx context.Context, previous, next domainsession.Credential, recoveryExpiresAt time.Time) error {
	result, err := r.tx.Exec(ctx, `
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
		return domainsession.ErrNotFound
	}
	_, err = r.tx.Exec(ctx, `
		INSERT INTO auth_session_credentials (
			session_credential_id, session_id, credential_type, credential_status, secret_hash,
			secret_key_version, csrf_key_version, csrf_token_hash, refresh_family_id, rotated_from_credential_id,
			issued_at, expires_at, created_at
		) VALUES ($1, $2, $3, 'active', $4, 1, $5, $6, $7, $8, now(), $9, now())
	`, next.ID, next.SessionID, next.Type, next.SecretHash, sessionCSRFVersion(next), nullableSessionBytes(next.CSRFHash), next.FamilyID, previous.ID, next.ExpiresAt)
	return err
}

func (r *SessionTxRepository) Rebind(ctx context.Context, current domainsession.Session) error {
	result, err := r.tx.Exec(ctx, `
		UPDATE auth_sessions
		SET identity_id = $2, identity_link_id = $3, authentication_method = $4,
			last_authenticated_at = now(), updated_at = now(), row_version = row_version + 1
		WHERE session_id = $1 AND session_status = 'active'
	`, current.ID, current.IdentityID, current.IdentityLink, current.Method)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domainsession.ErrNotFound
	}
	return nil
}

func (r *SessionTxRepository) Revoke(ctx context.Context, sessionID uuid.UUID, reason string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $2, updated_at = now()
		WHERE session_id = $1 AND session_status = 'active'
	`, sessionID, reason)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'revoked', revoked_at = now()
		WHERE session_id = $1 AND credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, sessionID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_reauth_proofs
		SET invalidated_at = now()
		WHERE session_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL
	`, sessionID)
	return err
}

func (r *SessionTxRepository) RevokeForUser(ctx context.Context, userID uuid.UUID, reason string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $2, updated_at = now()
		WHERE user_id = $1 AND session_status = 'active'
	`, userID, reason)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_session_credentials c
		SET credential_status = 'revoked', revoked_at = now()
		FROM auth_sessions s
		WHERE c.session_id = s.session_id AND s.user_id = $1 AND c.credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, userID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_reauth_proofs p
		SET invalidated_at = now()
		FROM auth_sessions s
		WHERE p.session_id = s.session_id AND s.user_id = $1 AND p.consumed_at IS NULL AND p.invalidated_at IS NULL
	`, userID)
	return err
}

func (r *SessionTxRepository) RevokeForIdentityLinkExcept(ctx context.Context, identityLinkID, keepSessionID uuid.UUID, reason string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = $3, updated_at = now()
		WHERE identity_link_id = $1 AND session_id <> $2 AND session_status = 'active'
	`, identityLinkID, keepSessionID, reason)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_session_credentials c
		SET credential_status = 'revoked', revoked_at = now()
		FROM auth_sessions s
		WHERE c.session_id = s.session_id AND s.identity_link_id = $1 AND s.session_id <> $2
			AND c.credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, identityLinkID, keepSessionID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_reauth_proofs p
		SET invalidated_at = now()
		FROM auth_sessions s
		WHERE p.session_id = s.session_id AND s.identity_link_id = $1 AND s.session_id <> $2
			AND p.consumed_at IS NULL AND p.invalidated_at IS NULL
	`, identityLinkID, keepSessionID)
	return err
}

func (r *SessionTxRepository) MarkReuseDetected(ctx context.Context, sessionID, familyID uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_session_credentials
		SET credential_status = 'reuse_detected', reuse_detected_at = now(), revoked_at = now()
		WHERE refresh_family_id = $1 AND credential_status IN ('active', 'rotated', 'rotated_pending_delivery')
	`, familyID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'reuse_detected', reuse_detected_at = now(), revocation_reason = 'refresh_reuse', updated_at = now()
		WHERE session_id = $1
	`, sessionID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_reauth_proofs
		SET invalidated_at = now()
		WHERE session_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL
	`, sessionID)
	return err
}

func scanSessionCredential(row pgx.Row) (domainsession.Session, domainsession.Credential, error) {
	var current domainsession.Session
	var credential domainsession.Credential
	err := row.Scan(
		&current.ID, &current.UserID, &current.IdentityID, &current.IdentityLink, &current.Method,
		&current.Channel, &current.RememberMe, &current.AuthenticatedAt, &current.ExpiresAt, &current.Status, &current.Version,
		&credential.ID, &credential.SessionID, &credential.Type, &credential.Status,
		&credential.SecretHash, &credential.CSRFHash, &credential.FamilyID, &credential.ExpiresAt, &credential.DeliveryRecoveryExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainsession.Session{}, domainsession.Credential{}, domainsession.ErrNotFound
	}
	return current, credential, err
}

func scanSession(row pgx.Row) (domainsession.Session, error) {
	var current domainsession.Session
	err := row.Scan(
		&current.ID, &current.UserID, &current.IdentityID, &current.IdentityLink, &current.Method,
		&current.Channel, &current.RememberMe, &current.AuthenticatedAt, &current.ExpiresAt, &current.Status, &current.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainsession.Session{}, domainsession.ErrNotFound
	}
	return current, err
}

func scanSessions(rows pgx.Rows) ([]domainsession.Session, error) {
	defer rows.Close()
	result := make([]domainsession.Session, 0)
	for rows.Next() {
		current, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, current)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func nullableSessionBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return value
}

func sessionCSRFVersion(credential domainsession.Credential) *int16 {
	if credential.Type != "web_refresh_cookie" {
		return nil
	}
	version := int16(1)
	return &version
}

var _ domainsession.Repository = (*SessionRepository)(nil)
var _ applicationsession.Repository = (*SessionTxRepository)(nil)
