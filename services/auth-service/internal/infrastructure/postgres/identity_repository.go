package postgres

import (
	"context"
	"errors"

	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type IdentityRepository struct {
	tx pgx.Tx
}

func NewIdentityRepository(tx pgx.Tx) *IdentityRepository {
	return &IdentityRepository{tx: tx}
}

func (r *IdentityRepository) Reserve(ctx context.Context, value domainidentity.Identity) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_identities (
			identity_id, identity_type, identity_namespace, normalized_value, masked_value,
			status, verification_status, credential_status, created_at, updated_at
		) VALUES ($1, $2, 'default', $3, $4, 'pending', 'pending', 'active', now(), now())
	`, value.ID, value.Type, value.NormalizedValue, value.MaskedValue)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) FindByValueForUpdate(ctx context.Context, identityType domainidentity.Type, normalized string) (domainidentity.Identity, error) {
	var result domainidentity.Identity
	err := r.tx.QueryRow(ctx, `
		SELECT identity_id, identity_type, normalized_value, masked_value, status, credential_status
		FROM auth_identities
		WHERE identity_type = $1 AND identity_namespace = 'default' AND normalized_value = $2
		FOR UPDATE
	`, identityType, normalized).Scan(&result.ID, &result.Type, &result.NormalizedValue, &result.MaskedValue, &result.Status, &result.CredentialState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainidentity.Identity{}, domainidentity.ErrNotFound
	}
	return result, err
}

func (r *IdentityRepository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (domainidentity.Identity, error) {
	var result domainidentity.Identity
	err := r.tx.QueryRow(ctx, `
		SELECT identity_id, identity_type, normalized_value, masked_value, status, credential_status
		FROM auth_identities
		WHERE identity_id = $1
		FOR UPDATE
	`, id).Scan(&result.ID, &result.Type, &result.NormalizedValue, &result.MaskedValue, &result.Status, &result.CredentialState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainidentity.Identity{}, domainidentity.ErrNotFound
	}
	return result, err
}

func (r *IdentityRepository) MarkVerified(ctx context.Context, id uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_identities
		SET status = 'verified', verification_status = 'verified', verified_at = now(), row_version = row_version + 1, updated_at = now()
		WHERE identity_id = $1
	`, id)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) CreatePasswordCredential(ctx context.Context, identityID uuid.UUID, hash string) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_password_credentials (
			password_credential_id, identity_id, password_hash, password_status, hash_algorithm, created_at, updated_at
		) VALUES ($1, $2, $3, 'active', 'bcrypt', now(), now())
	`, uuid.New(), identityID, hash)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) ReplacePasswordCredential(ctx context.Context, identityID uuid.UUID, hash string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_password_credentials
		SET password_status = 'replaced', replaced_at = now(), updated_at = now()
		WHERE identity_id = $1 AND password_status = 'active'
	`, identityID)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		INSERT INTO auth_password_credentials (
			password_credential_id, identity_id, password_hash, password_status, hash_algorithm, created_at, updated_at
		) VALUES ($1, $2, $3, 'active', 'bcrypt', now(), now())
	`, uuid.New(), identityID, hash)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) FindEmailCredentialForUpdate(ctx context.Context, email string) (domainidentity.Identity, domainidentity.Link, domainidentity.PasswordCredential, error) {
	var identity domainidentity.Identity
	var link domainidentity.Link
	var credential domainidentity.PasswordCredential
	err := r.tx.QueryRow(ctx, `
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
		return domainidentity.Identity{}, domainidentity.Link{}, domainidentity.PasswordCredential{}, domainidentity.ErrNotFound
	}
	return identity, link, credential, err
}

func (r *IdentityRepository) FindActivePhoneLinkForUpdate(ctx context.Context, phone string) (domainidentity.Identity, domainidentity.Link, error) {
	var identity domainidentity.Identity
	var link domainidentity.Link
	err := r.tx.QueryRow(ctx, `
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
		return domainidentity.Identity{}, domainidentity.Link{}, domainidentity.ErrNotFound
	}
	return identity, link, err
}

func (r *IdentityRepository) CreateActiveLink(ctx context.Context, link domainidentity.Link) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
			activated_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'active', 'signup', now(), now(), now())
	`, link.ID, link.Identity, link.Type, link.UserID)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) CreateRequestedLink(ctx context.Context, link domainidentity.Link) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
			intent_expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'requested', 'signin_link', $5, now(), now())
	`, link.ID, link.Identity, link.Type, link.UserID, link.ExpiresAt)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) CreatePhoneReplacementRequested(ctx context.Context, link domainidentity.Link, previousLinkID, reauthProofID uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
			reauthentication_proof_id, previous_identity_link_id, intent_expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'requested', 'phone_change', $5, $6, $7, now(), now())
	`, link.ID, link.Identity, link.Type, link.UserID, reauthProofID, previousLinkID, link.ExpiresAt)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) AttachProofChallenge(ctx context.Context, linkID, challengeID uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_identity_links SET proof_challenge_id = $2, row_version = row_version + 1, updated_at = now()
		WHERE identity_link_id = $1 AND link_status = 'requested'
	`, linkID, challengeID)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) ActivateLink(ctx context.Context, id uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'active', activated_at = now(), updated_at = now(), row_version = row_version + 1
		WHERE identity_link_id = $1 AND link_status = 'requested' AND intent_expires_at > now()
	`, id)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) ReplacePhoneLink(ctx context.Context, previous, replacement uuid.UUID) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'replaced', closed_at = now(), closed_reason = 'phone_replaced', updated_at = now()
		WHERE identity_link_id = $1 AND link_status = 'active'
	`, previous)
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'active', activated_at = now(), updated_at = now()
		WHERE identity_link_id = $1 AND link_status = 'requested'
	`, replacement)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) RevokeLinksForUser(ctx context.Context, userID uuid.UUID, identityType domainidentity.Type, reason string) error {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_identity_links
		SET link_status = 'revoked', closed_at = now(), closed_reason = $3, updated_at = now()
		WHERE user_id = $1 AND identity_type = $2 AND link_status = 'active'
	`, userID, identityType, reason)
	return mapIdentityRepositoryError(err)
}

func (r *IdentityRepository) RequestedLinkForUpdate(ctx context.Context, id uuid.UUID) (domainidentity.Link, domainidentity.Identity, error) {
	var link domainidentity.Link
	var identity domainidentity.Identity
	err := r.tx.QueryRow(ctx, `
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
		return domainidentity.Link{}, domainidentity.Identity{}, domainidentity.ErrNotFound
	}
	return link, identity, err
}

func (r *IdentityRepository) FindActiveLinkForIdentityUser(ctx context.Context, identityID, userID uuid.UUID) (domainidentity.Link, error) {
	var link domainidentity.Link
	err := r.tx.QueryRow(ctx, `
		SELECT identity_link_id, identity_id, user_id, identity_type, link_status, COALESCE(intent_expires_at, now())
		FROM auth_identity_links
		WHERE identity_id = $1 AND user_id = $2 AND link_status = 'active'
		FOR UPDATE
	`, identityID, userID).Scan(&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainidentity.Link{}, domainidentity.ErrNotFound
	}
	return link, err
}

func (r *IdentityRepository) FindActiveLinkForIdentity(ctx context.Context, identityID uuid.UUID) (domainidentity.Link, error) {
	var link domainidentity.Link
	err := r.tx.QueryRow(ctx, `
		SELECT identity_link_id, identity_id, user_id, identity_type, link_status, intent_expires_at
		FROM auth_identity_links
		WHERE identity_id = $1 AND link_status = 'active'
		FOR UPDATE
	`, identityID).Scan(&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainidentity.Link{}, domainidentity.ErrNotFound
	}
	return link, err
}

func (r *IdentityRepository) FindActiveLinkForUserType(ctx context.Context, userID uuid.UUID, identityType domainidentity.Type) (domainidentity.Link, domainidentity.Identity, error) {
	var link domainidentity.Link
	var identity domainidentity.Identity
	err := r.tx.QueryRow(ctx, `
		SELECT l.identity_link_id, l.identity_id, l.user_id, l.identity_type, l.link_status, l.intent_expires_at,
			i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status
		FROM auth_identity_links l JOIN auth_identities i ON i.identity_id = l.identity_id
		WHERE l.user_id = $1 AND l.identity_type = $2 AND l.link_status = 'active'
		FOR UPDATE OF l, i
	`, userID, identityType).Scan(&link.ID, &link.Identity, &link.UserID, &link.Type, &link.Status, &link.ExpiresAt, &identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainidentity.Link{}, domainidentity.Identity{}, domainidentity.ErrNotFound
	}
	return link, identity, err
}

func (r *IdentityRepository) FindActiveEmailCredentialForUser(ctx context.Context, userID uuid.UUID) (domainidentity.Identity, domainidentity.PasswordCredential, error) {
	var identity domainidentity.Identity
	var credential domainidentity.PasswordCredential
	err := r.tx.QueryRow(ctx, `
		SELECT i.identity_id, i.identity_type, i.normalized_value, i.masked_value, i.status, i.credential_status,
			p.identity_id, p.password_hash, p.password_status
		FROM auth_identity_links l JOIN auth_identities i ON i.identity_id=l.identity_id
		JOIN auth_password_credentials p ON p.identity_id=i.identity_id AND p.password_status='active'
		WHERE l.user_id=$1 AND l.identity_type='email' AND l.link_status='active'
		FOR UPDATE OF l, i, p
	`, userID).Scan(&identity.ID, &identity.Type, &identity.NormalizedValue, &identity.MaskedValue, &identity.Status, &identity.CredentialState, &credential.IdentityID, &credential.Hash, &credential.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainidentity.Identity{}, domainidentity.PasswordCredential{}, domainidentity.ErrNotFound
	}
	return identity, credential, err
}

func mapIdentityRepositoryError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return domainidentity.ErrConflict
	}
	return err
}
