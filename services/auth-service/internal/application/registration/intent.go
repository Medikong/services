package registration

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

func (s *Service) verifyActiveIntent(ctx context.Context, repositories TxRepositories, intentID uuid.UUID, ownerProof, csrf string, requireCSRF bool) (domainintent.Intent, error) {
	current, err := repositories.Intents.FindActiveForUpdate(ctx, intentID)
	if errors.Is(err, domainintent.ErrNotFound) {
		return domainintent.Intent{}, failure.NotFound("AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return domainintent.Intent{}, unavailable(err)
	}
	return s.intentProof.VerifyOwnership(current, ownerProof, csrf, requireCSRF)
}

func (s *Service) verifyReplayIntent(ctx context.Context, repositories TxRepositories, intentID, sessionID uuid.UUID, ownerProof, csrf string) (domainintent.Intent, error) {
	current, err := repositories.Intents.FindCompletionReplayForUpdate(ctx, intentID, sessionID)
	if errors.Is(err, domainintent.ErrNotFound) {
		return domainintent.Intent{}, failure.NotFound("AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return domainintent.Intent{}, unavailable(err)
	}
	return s.intentProof.VerifyOwnership(current, ownerProof, csrf, true)
}
