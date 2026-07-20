package registration

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

func (s *Service) Status(ctx context.Context, input StatusInput) (StatusOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	if err != nil {
		return StatusOutput{}, failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}

	var output StatusOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		registration, findErr := repositories.Registrations.Find(ctx, registrationID)
		if errors.Is(findErr, domainregistration.ErrNotFound) {
			return failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		authorized := false
		if strings.TrimSpace(input.OwnerProof) != "" {
			_, verifyErr := s.verifyActiveIntent(ctx, repositories, registration.IntentID, input.OwnerProof, input.CSRFToken, false)
			authorized = verifyErr == nil
		} else if strings.TrimSpace(input.StatusToken) != "" && registration.StatusTokenExpires.After(s.clock.Now().UTC()) {
			authorized = s.cryptography.Equal(registration.StatusTokenHash, registration.ID.String(), input.StatusToken)
		}
		if !authorized {
			return failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
		}
		output = StatusOutput{
			RegistrationID: registration.ID.String(), Status: registration.Status,
			VerifiedMethods: registrationVerifiedMethods(registration), Retryable: registrationRetryable(registration.Status),
			ExpiresAt: registration.ExpiresAt,
		}
		return nil
	})
	if err != nil {
		return StatusOutput{}, preserveFailure(err)
	}
	return output, nil
}
