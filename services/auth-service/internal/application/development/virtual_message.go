// Package development contains dev/test-only application services. It is
// composed only when the validated development feature gate is enabled.
package development

import (
	"context"
	"errors"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	"github.com/Medikong/services/services/auth-service/internal/application/bootstrap"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	resetdomain "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	registrationdomain "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VirtualMessageService struct {
	pool          *pgxpool.Pool
	keys          security.Keys
	bootstrap     *bootstrap.Service
	challenges    challenge.Repository
	registrations registrationdomain.Repository
	resets        resetdomain.Repository
	identities    identity.Repository
}

func NewVirtualMessageService(pool *pgxpool.Pool, keys security.Keys, bootstrap *bootstrap.Service, challenges challenge.Repository, registrations registrationdomain.Repository, resets resetdomain.Repository, identities identity.Repository) *VirtualMessageService {
	return &VirtualMessageService{pool: pool, keys: keys, bootstrap: bootstrap, challenges: challenges, registrations: registrations, resets: resets, identities: identities}
}

type VirtualMessageInput struct {
	ChallengeID string
	OwnerProof  string
	CSRFToken   string
	SessionUser *uuid.UUID
}

type VirtualMessageOutput struct {
	ChallengeID       string
	Channel           string
	Status            string
	Code              string
	MaskedDestination string
	ExpiresAt         time.Time
}

func (s *VirtualMessageService) Get(ctx context.Context, input VirtualMessageInput) (VirtualMessageOutput, error) {
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return VirtualMessageOutput{}, virtualNotFound()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VirtualMessageOutput{}, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, err := s.challenges.Find(ctx, tx, challengeID)
	if errors.Is(err, challenge.ErrNotFound) {
		return VirtualMessageOutput{}, virtualNotFound()
	}
	if err != nil {
		return VirtualMessageOutput{}, application.Unavailable()
	}
	if !s.ownsChallenge(ctx, tx, current, input) {
		return VirtualMessageOutput{}, virtualNotFound()
	}
	projection, err := s.challenges.FindVirtualProjection(ctx, tx, challengeID, time.Now().UTC())
	if errors.Is(err, challenge.ErrVirtualUnavailable) {
		return VirtualMessageOutput{}, application.Problem(410, "AUTH_VIRTUAL_MESSAGE_UNAVAILABLE", "가상 인증 메시지를 더 이상 사용할 수 없습니다.")
	}
	if err != nil {
		return VirtualMessageOutput{}, application.Unavailable()
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := s.keys.OpenVirtual(projection.CodeCiphertext, &payload); err != nil || len(payload.Code) != 6 {
		return VirtualMessageOutput{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return VirtualMessageOutput{}, application.Unavailable()
	}
	return VirtualMessageOutput{ChallengeID: challengeID.String(), Channel: string(projection.Channel), Status: string(projection.Status), Code: payload.Code, MaskedDestination: projection.MaskedDestination, ExpiresAt: projection.ExpiresAt}, nil
}

func (s *VirtualMessageService) ownsChallenge(ctx context.Context, tx pgx.Tx, current challenge.Challenge, input VirtualMessageInput) bool {
	if current.SubjectType == challenge.SubjectRegistration {
		registration, err := s.registrations.Find(ctx, tx, current.SubjectID)
		if err != nil {
			return false
		}
		_, err = s.bootstrap.VerifyOwnershipTx(ctx, tx, registration.IntentID, input.OwnerProof, input.CSRFToken, false)
		return err == nil
	}
	if current.SubjectType == challenge.SubjectPasswordReset {
		reset, err := s.resets.FindForUpdate(ctx, tx, current.SubjectID)
		if err != nil || reset.IntentID == nil {
			return false
		}
		_, err = s.bootstrap.VerifyOwnershipTx(ctx, tx, *reset.IntentID, input.OwnerProof, input.CSRFToken, false)
		return err == nil
	}
	if current.SubjectType == challenge.SubjectPhoneSignIn {
		_, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, current.SubjectID, input.OwnerProof, input.CSRFToken, false)
		return err == nil
	}
	if current.SubjectType == challenge.SubjectIdentityLink || current.SubjectType == challenge.SubjectPhoneChange {
		if input.SessionUser == nil {
			return false
		}
		link, _, err := s.identities.RequestedLinkForUpdate(ctx, tx, current.SubjectID)
		return err == nil && link.UserID == *input.SessionUser
	}
	return false
}

func virtualNotFound() *application.Error {
	return application.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다.")
}
