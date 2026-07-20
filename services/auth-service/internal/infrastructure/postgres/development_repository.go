package postgres

import (
	"context"
	"time"

	applicationdevelopment "github.com/Medikong/services/services/auth-service/internal/application/development"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
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

var _ applicationdevelopment.Repository = (*DevelopmentRepository)(nil)
