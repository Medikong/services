package postgres

import (
	"context"
	"time"

	applicationdevelopment "github.com/Medikong/services/services/auth-service/internal/application/development"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type DevelopmentRepository struct {
	tx         pgx.Tx
	challenges *ChallengeRepository
	intents    *IntentRepository
}

func NewDevelopmentRepository(tx pgx.Tx) *DevelopmentRepository {
	return &DevelopmentRepository{
		tx:         tx,
		challenges: NewChallengeRepository(tx, ChallengeOptions{VirtualProjectionEnabled: true}),
		intents:    NewIntentRepository(tx),
	}
}

func (r *DevelopmentRepository) FindChallenge(ctx context.Context, id uuid.UUID) (domainchallenge.Challenge, error) {
	return r.challenges.Find(ctx, id)
}

func (r *DevelopmentRepository) FindVirtualProjection(ctx context.Context, id uuid.UUID, now time.Time) (domainchallenge.VirtualProjection, error) {
	return r.challenges.FindVirtualProjection(ctx, id, now)
}

func (r *DevelopmentRepository) FindRegistrationIntent(ctx context.Context, registrationID uuid.UUID) (uuid.UUID, error) {
	var intentID uuid.UUID
	err := r.tx.QueryRow(ctx, `
		SELECT intent_id
		FROM auth_registrations
		WHERE registration_id = $1
	`, registrationID).Scan(&intentID)
	return intentID, err
}

func (r *DevelopmentRepository) FindPasswordResetIntent(ctx context.Context, resetID uuid.UUID) (uuid.UUID, error) {
	var intentID uuid.UUID
	err := r.tx.QueryRow(ctx, `
		SELECT intent_id
		FROM auth_password_resets
		WHERE password_reset_id = $1 AND intent_id IS NOT NULL
		FOR UPDATE
	`, resetID).Scan(&intentID)
	return intentID, err
}

func (r *DevelopmentRepository) FindRequestedLinkUser(ctx context.Context, linkID uuid.UUID) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.tx.QueryRow(ctx, `
		SELECT user_id
		FROM auth_identity_links
		WHERE identity_link_id = $1 AND link_status = 'requested'
		FOR UPDATE
	`, linkID).Scan(&userID)
	return userID, err
}

func (r *DevelopmentRepository) FindIntentForUpdate(ctx context.Context, intentID uuid.UUID) (domainintent.Intent, error) {
	return r.intents.FindActiveForUpdate(ctx, intentID)
}

func (r *DevelopmentRepository) CreatePrincipalsBulk(ctx context.Context, principals []applicationdevelopment.PrincipalInput) error {
	if len(principals) == 0 {
		return nil
	}
	now := time.Now().UTC()
	if _, err := r.tx.CopyFrom(ctx, pgx.Identifier{"auth_identities"}, []string{
		"identity_id", "identity_type", "identity_namespace", "normalized_value", "masked_value",
		"status", "verification_status", "credential_status", "verified_at", "created_at", "updated_at",
	}, pgx.CopyFromSlice(len(principals), func(index int) ([]any, error) {
		principal := principals[index]
		return []any{principal.IdentityID, "email", "default", principal.Email, "d***@example.invalid", "verified", "verified", "active", now, now, now}, nil
	})); err != nil {
		return err
	}
	if _, err := r.tx.CopyFrom(ctx, pgx.Identifier{"auth_identity_links"}, []string{
		"identity_link_id", "identity_id", "identity_type", "user_id", "link_status", "link_reason", "activated_at", "created_at", "updated_at",
	}, pgx.CopyFromSlice(len(principals), func(index int) ([]any, error) {
		principal := principals[index]
		return []any{principal.LinkID, principal.IdentityID, "email", principal.UserID, "active", "signup", now, now, now}, nil
	})); err != nil {
		return err
	}
	if _, err := r.tx.CopyFrom(ctx, pgx.Identifier{"auth_user_auth_states"}, []string{
		"user_id", "status", "user_version", "status_change_id", "effective_at", "updated_at",
	}, pgx.CopyFromSlice(len(principals), func(index int) ([]any, error) {
		principal := principals[index]
		return []any{principal.UserID, "active", int64(1), principal.ChangeID, now, now}, nil
	})); err != nil {
		return err
	}
	return nil
}

func (r *DevelopmentRepository) SessionBulkRepositories() applicationsession.BulkTxRepositories {
	return applicationsession.BulkTxRepositories{Sessions: r, UserAuthState: r}
}

func (r *DevelopmentRepository) FindActiveForUpdate(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	rows, err := r.tx.Query(ctx, `
		SELECT user_id
		FROM auth_user_auth_states
		WHERE user_id = ANY($1) AND status = 'active'
		FOR UPDATE
	`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := make(map[uuid.UUID]struct{}, len(userIDs))
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		active[userID] = struct{}{}
	}
	return active, rows.Err()
}

func (r *DevelopmentRepository) CreateAccessSessionsBulk(ctx context.Context, sessions []domainsession.Session) error {
	if len(sessions) == 0 {
		return nil
	}
	now := time.Now().UTC()
	_, err := r.tx.CopyFrom(ctx, pgx.Identifier{"auth_sessions"}, []string{
		"session_id", "user_id", "identity_id", "identity_link_id", "authentication_method", "session_status",
		"client_channel", "remember_me", "issued_at", "absolute_expires_at", "created_at", "updated_at",
	}, pgx.CopyFromSlice(len(sessions), func(index int) ([]any, error) {
		current := sessions[index]
		return []any{
			current.ID, current.UserID, current.IdentityID, current.IdentityLink, current.Method, "active",
			string(current.Channel), current.RememberMe, now, current.ExpiresAt, now, now,
		}, nil
	}))
	return err
}

var _ applicationdevelopment.Repository = (*DevelopmentRepository)(nil)
var _ applicationdevelopment.FixtureRepository = (*DevelopmentRepository)(nil)
