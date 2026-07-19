package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("identity not found")
	ErrConflict = errors.New("identity already exists")
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Reserve(ctx context.Context, tx pgx.Tx, value Identity) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_identities (
			identity_id, identity_type, identity_namespace, normalized_value, masked_value,
			status, verification_status, credential_status, created_at, updated_at
		) VALUES ($1, $2, 'default', $3, $4, 'pending', 'pending', 'active', now(), now())
	`, value.ID, value.Type, value.NormalizedValue, value.MaskedValue)
	return err
}

func (r *PostgresRepository) FindByValueForUpdate(ctx context.Context, tx pgx.Tx, identityType Type, normalized string) (Identity, error) {
	var result Identity
	err := tx.QueryRow(ctx, `
		SELECT identity_id, identity_type, normalized_value, masked_value, status, credential_status
		FROM auth_identities
		WHERE identity_type = $1 AND identity_namespace = 'default' AND normalized_value = $2
		FOR UPDATE
	`, identityType, normalized).Scan(&result.ID, &result.Type, &result.NormalizedValue, &result.MaskedValue, &result.Status, &result.CredentialState)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	return result, err
}

func (r *PostgresRepository) FindByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Identity, error) {
	var result Identity
	err := tx.QueryRow(ctx, `
		SELECT identity_id, identity_type, normalized_value, masked_value, status, credential_status
		FROM auth_identities
		WHERE identity_id = $1
		FOR UPDATE
	`, id).Scan(&result.ID, &result.Type, &result.NormalizedValue, &result.MaskedValue, &result.Status, &result.CredentialState)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	return result, err
}

func (r *PostgresRepository) MarkVerified(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_identities
		SET status = 'verified', verification_status = 'verified', verified_at = now(), row_version = row_version + 1, updated_at = now()
		WHERE identity_id = $1
	`, id)
	return err
}

func (r *PostgresRepository) CreatePasswordCredential(ctx context.Context, tx pgx.Tx, identityID uuid.UUID, hash string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_password_credentials (
			password_credential_id, identity_id, password_hash, password_status, hash_algorithm, created_at, updated_at
		) VALUES ($1, $2, $3, 'active', 'argon2id', now(), now())
	`, uuid.New(), identityID, hash)
	return err
}

func (r *PostgresRepository) ReplacePasswordCredential(ctx context.Context, tx pgx.Tx, identityID uuid.UUID, hash string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_password_credentials
		SET password_hash = NULL, password_status = 'replaced', replaced_at = now(), updated_at = now()
		WHERE identity_id = $1 AND password_status = 'active'
	`, identityID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_identities
		SET credential_status = 'active', password_reset_required_at = NULL,
			password_reset_reason = NULL, row_version = row_version + 1, updated_at = now()
		WHERE identity_id = $1 AND credential_status = 'password_reset_required'
	`, identityID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth_password_credentials (
			password_credential_id, identity_id, password_hash, password_status, hash_algorithm, created_at, updated_at
		) VALUES ($1, $2, $3, 'active', 'argon2id', now(), now())
	`, uuid.New(), identityID, hash)
	return err
}

func (r *PostgresRepository) FindEmailCredentialForUpdate(ctx context.Context, tx pgx.Tx, email string) (Identity, Link, PasswordCredential, error) {
	var identity Identity
	var link Link
	var credential PasswordCredential
	err := tx.QueryRow(ctx, `
		SELECT i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status,
			l.identity_link_id, l.identity_id, l.user_id, l.identity_type, l.link_status,
			p.identity_id, p.password_hash, p.password_status
		FROM auth_identities i
		JOIN auth_identity_links l ON l.identity_id = i.identity_id AND l.link_status = 'active'
		JOIN auth_password_credentials p ON p.identity_id = i.identity_id AND p.password_status = 'active'
		WHERE i.identity_type = 'email' AND i.normalized_value = $1
		FOR UPDATE
	`, email).Scan(
		&identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState,
		&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status,
		&credential.IdentityID, &credential.Hash, &credential.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, Link{}, PasswordCredential{}, ErrNotFound
	}
	return identity, link, credential, err
}

func (r *PostgresRepository) FindActivePhoneLinkForUpdate(ctx context.Context, tx pgx.Tx, phone string) (Identity, Link, error) {
	var identity Identity
	var link Link
	err := tx.QueryRow(ctx, `
		SELECT i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status,
			l.identity_link_id, l.identity_id, l.user_id, l.identity_type, l.link_status
		FROM auth_identities i
		JOIN auth_identity_links l ON l.identity_id = i.identity_id AND l.link_status = 'active'
		WHERE i.identity_type = 'phone' AND i.normalized_value = $1
		FOR UPDATE
	`, phone).Scan(
		&identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState,
		&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, Link{}, ErrNotFound
	}
	return identity, link, err
}

func (r *PostgresRepository) CreateActiveLink(ctx context.Context, tx pgx.Tx, link Link) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
			activated_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'active', 'signup', now(), now(), now())
	`, link.ID, link.Identity, link.Type, link.UserID)
	return err
}

func (r *PostgresRepository) CreateRequestedLink(ctx context.Context, tx pgx.Tx, link Link) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
			intent_expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'requested', 'signin_link', $5, now(), now())
	`, link.ID, link.Identity, link.Type, link.UserID, link.ExpiresAt)
	return err
}

func (r *PostgresRepository) CreatePhoneReplacementRequested(ctx context.Context, tx pgx.Tx, link Link, previousLinkID, reauthProofID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
			reauthentication_proof_id, previous_identity_link_id, intent_expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'requested', 'phone_change', $5, $6, $7, now(), now())
	`, link.ID, link.Identity, link.Type, link.UserID, reauthProofID, previousLinkID, link.ExpiresAt)
	return err
}

func (r *PostgresRepository) AttachProofChallenge(ctx context.Context, tx pgx.Tx, linkID, challengeID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_identity_links SET proof_challenge_id = $2, row_version = row_version + 1, updated_at = now()
		WHERE identity_link_id = $1 AND link_status = 'requested'
	`, linkID, challengeID)
	return err
}

func (r *PostgresRepository) ActivateLink(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'active', activated_at = now(), updated_at = now(), row_version = row_version + 1
		WHERE identity_link_id = $1 AND link_status = 'requested' AND intent_expires_at > now()
	`, id)
	return err
}

func (r *PostgresRepository) ReplacePhoneLink(ctx context.Context, tx pgx.Tx, previous, replacement uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'replaced', closed_at = now(), closed_reason = 'phone_replaced', updated_at = now()
		WHERE identity_link_id = $1 AND link_status = 'active'
	`, previous)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'active', activated_at = now(), updated_at = now()
		WHERE identity_link_id = $1 AND link_status = 'requested'
	`, replacement)
	return err
}

func (r *PostgresRepository) RevokeLinksForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID, identityType Type, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'revoked', closed_at = now(), closed_reason = $3, updated_at = now()
		WHERE user_id = $1 AND identity_type = $2 AND link_status = 'active'
	`, userID, identityType, reason)
	return err
}

func (r *PostgresRepository) RequestedLinkForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Link, Identity, error) {
	var link Link
	var identity Identity
	err := tx.QueryRow(ctx, `
		SELECT l.identity_link_id, l.identity_id, l.user_id, l.identity_type, l.link_status, l.intent_expires_at, l.previous_identity_link_id,
			i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status
		FROM auth_identity_links l JOIN auth_identities i ON i.identity_id = l.identity_id
		WHERE l.identity_link_id = $1 AND l.link_status = 'requested'
		FOR UPDATE
	`, id).Scan(
		&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt, &link.PreviousID,
		&identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, Identity{}, ErrNotFound
	}
	return link, identity, err
}

func (r *PostgresRepository) FindActiveLinkForIdentityUser(ctx context.Context, tx pgx.Tx, identityID, userID uuid.UUID) (Link, error) {
	var link Link
	err := tx.QueryRow(ctx, `
		SELECT identity_link_id, identity_id, user_id, identity_type, link_status, COALESCE(intent_expires_at, now())
		FROM auth_identity_links
		WHERE identity_id = $1 AND user_id = $2 AND link_status = 'active'
		FOR UPDATE
	`, identityID, userID).Scan(&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return link, err
}

func (r *PostgresRepository) FindActiveLinkForIdentity(ctx context.Context, tx pgx.Tx, identityID uuid.UUID) (Link, error) {
	var link Link
	err := tx.QueryRow(ctx, `
		SELECT identity_link_id, identity_id, user_id, identity_type, link_status, intent_expires_at
		FROM auth_identity_links
		WHERE identity_id = $1 AND link_status = 'active'
		FOR UPDATE
	`, identityID).Scan(&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return link, err
}

func (r *PostgresRepository) FindActiveLinkForUserType(ctx context.Context, tx pgx.Tx, userID uuid.UUID, identityType Type) (Link, Identity, error) {
	var link Link
	var identity Identity
	err := tx.QueryRow(ctx, `
		SELECT l.identity_link_id, l.identity_id, l.user_id, l.identity_type, l.link_status, l.intent_expires_at,
			i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status
		FROM auth_identity_links l JOIN auth_identities i ON i.identity_id = l.identity_id
		WHERE l.user_id = $1 AND l.identity_type = $2 AND l.link_status = 'active'
		FOR UPDATE OF l, i
	`, userID, identityType).Scan(&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt, &identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, Identity{}, ErrNotFound
	}
	return link, identity, err
}

func (r *PostgresRepository) FindActiveEmailCredentialForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (Identity, PasswordCredential, error) {
	var identity Identity
	var credential PasswordCredential
	err := tx.QueryRow(ctx, `
		SELECT i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status,
			p.identity_id, p.password_hash, p.password_status
		FROM auth_identity_links l JOIN auth_identities i ON i.identity_id=l.identity_id
		JOIN auth_password_credentials p ON p.identity_id=i.identity_id AND p.password_status='active'
		WHERE l.user_id=$1 AND l.identity_type='email' AND l.link_status='active'
		FOR UPDATE OF l, i, p
	`, userID).Scan(&identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState, &credential.IdentityID, &credential.Hash, &credential.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, PasswordCredential{}, ErrNotFound
	}
	return identity, credential, err
}
