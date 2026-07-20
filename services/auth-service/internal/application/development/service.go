package development

import (
	"context"
	"errors"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/google/uuid"
)

type Service struct {
	transactions Transactor
	cryptography Cryptography
	ownership    IntentOwnershipVerifier
	clock        Clock
}

func NewService(transactions Transactor, cryptography Cryptography, ownership IntentOwnershipVerifier, clock Clock) *Service {
	return &Service{transactions: transactions, cryptography: cryptography, ownership: ownership, clock: clock}
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

func (s *Service) GetVirtualMessage(ctx context.Context, input VirtualMessageInput) (VirtualMessageOutput, error) {
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return VirtualMessageOutput{}, virtualNotFound()
	}
	var output VirtualMessageOutput
	err = s.transactions.WithinTransaction(ctx, func(repository Repository) error {
		current, findErr := repository.FindChallenge(ctx, challengeID)
		if errors.Is(findErr, domainchallenge.ErrNotFound) {
			return virtualNotFound()
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		if !s.ownsChallenge(ctx, repository, current, input) {
			return virtualNotFound()
		}
		projection, projectionErr := repository.FindVirtualProjection(ctx, challengeID, s.clock.Now().UTC())
		if errors.Is(projectionErr, domainchallenge.ErrVirtualUnavailable) {
			return failure.New(failure.KindConflict, "AUTH_VIRTUAL_MESSAGE_UNAVAILABLE", "가상 인증 메시지를 더 이상 사용할 수 없습니다.")
		}
		if projectionErr != nil {
			return unavailable(projectionErr)
		}
		var payload struct {
			Code string `json:"code"`
		}
		if openErr := s.cryptography.OpenVirtual(projection.CodeCiphertext, &payload); openErr != nil || len(payload.Code) != 6 {
			return unavailable(openErr)
		}
		output = VirtualMessageOutput{
			ChallengeID: challengeID.String(), Channel: string(projection.Channel), Status: string(projection.Status),
			Code: payload.Code, MaskedDestination: projection.MaskedDestination, ExpiresAt: projection.ExpiresAt,
		}
		return nil
	})
	if err != nil {
		return VirtualMessageOutput{}, preserveFailure(err)
	}
	return output, nil
}

func (s *Service) ownsChallenge(ctx context.Context, repository Repository, current domainchallenge.Challenge, input VirtualMessageInput) bool {
	var intentID uuid.UUID
	var err error
	switch current.SubjectType {
	case domainchallenge.SubjectRegistration:
		intentID, err = repository.FindRegistrationIntent(ctx, current.SubjectID)
	case domainchallenge.SubjectPasswordReset:
		intentID, err = repository.FindPasswordResetIntent(ctx, current.SubjectID)
	case domainchallenge.SubjectPhoneSignIn:
		intentID = current.SubjectID
	case domainchallenge.SubjectIdentityLink, domainchallenge.SubjectPhoneChange:
		if input.SessionUser == nil {
			return false
		}
		userID, findErr := repository.FindRequestedLinkUser(ctx, current.SubjectID)
		return findErr == nil && userID == *input.SessionUser
	default:
		return false
	}
	if err != nil || intentID == uuid.Nil {
		return false
	}
	currentIntent, err := repository.FindIntentForUpdate(ctx, intentID)
	if err != nil {
		return false
	}
	_, err = s.ownership.VerifyOwnership(currentIntent, input.OwnerProof, input.CSRFToken, false)
	return err == nil
}

func virtualNotFound() error {
	return failure.NotFound("AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다.")
}
