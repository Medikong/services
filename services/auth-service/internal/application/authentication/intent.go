package authentication

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

func verifyIntent(ctx context.Context, repository IntentRepository, ownership OwnershipVerifier, intentID uuid.UUID, ownerProof, csrf string, requireCSRF bool) (domainintent.Intent, error) {
	current, err := repository.FindActiveForUpdate(ctx, intentID)
	if errors.Is(err, domainintent.ErrNotFound) {
		return domainintent.Intent{}, failure.NotFound("AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return domainintent.Intent{}, preserveFailure(err)
	}
	return ownership.VerifyOwnership(current, ownerProof, csrf, requireCSRF)
}
