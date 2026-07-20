package intent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

type ActionResumeService struct {
	transactor Transactor
	crypto     ActionResumeCryptography
	clock      Clock
}

func NewActionResumeService(transactor Transactor, crypto ActionResumeCryptography, clock Clock) *ActionResumeService {
	return &ActionResumeService{transactor: transactor, crypto: crypto, clock: clock}
}

type ResumeInput struct {
	Principal      Principal
	IntentID       string
	IdempotencyKey string
}

type ResumeOutput struct {
	IntentID      string
	Action        string
	ReturnPath    string
	ActionContext map[string]any
}

func (s *ActionResumeService) Resume(ctx context.Context, input ResumeInput) (ResumeOutput, error) {
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return ResumeOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "인증 후 행동 복구 요청이 올바르지 않습니다.")
	}
	if !input.Principal.Authenticated {
		return ResumeOutput{}, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "로그인 상태가 필요합니다.")
	}

	var output ResumeOutput
	err = s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		current, payload, findErr := repositories.Intents.FindConsumedActionForUpdate(ctx, intentID, input.Principal.SessionID)
		if errors.Is(findErr, domainintent.ErrNotFound) {
			return failure.New(failure.KindConflict, "AUTH_INTENT_EXPIRED", "인증 Intent를 더 이상 사용할 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}

		scopeHash := s.crypto.Hash("resume_authenticated_action", intentID.String(), input.Principal.SessionID.String())
		keyHash := s.crypto.Hash(input.IdempotencyKey)
		requestHash := s.crypto.Hash(intentID.String(), input.Principal.SessionID.String())
		record, findErr := repositories.Idempotency.FindForUpdate(ctx, "resume_authenticated_action", scopeHash, keyHash)
		if findErr == nil {
			if !bytes.Equal(record.RequestHash, requestHash) {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
			}
		} else if errors.Is(findErr, domainidempotency.ErrNotFound) {
			if payload.DeliveredAt != nil {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "이 행동은 이미 전달되었습니다.")
			}
			if markErr := repositories.Intents.MarkActionDelivered(ctx, payload.ID); markErr != nil {
				return unavailable(markErr)
			}
			newRecord := domainidempotency.NewRecord("resume_authenticated_action", scopeHash, keyHash, requestHash, &intentID, nil, s.clock.Now().UTC().Add(5*time.Minute))
			if createErr := repositories.Idempotency.CreateCompleted(ctx, newRecord, "AuthenticationIntent", "delivered"); createErr != nil {
				return unavailable(createErr)
			}
		} else {
			return unavailable(findErr)
		}

		var actionContext map[string]any
		if openErr := s.crypto.Open(payload.Ciphertext, &actionContext); openErr != nil {
			return unavailable(openErr)
		}
		if payload.ActionName != "purchase" || !validPurchase(actionContext) {
			return failure.Invalid("AUTH_INPUT_INVALID", "허용되지 않은 인증 후 행동입니다.")
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.action_resume.delivered", "user", input.Principal.UserID, intentID, map[string]string{"action": payload.ActionName}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		output = ResumeOutput{IntentID: intentID.String(), Action: payload.ActionName, ActionContext: actionContext, ReturnPath: current.ReturnPath}
		return nil
	})
	if err != nil {
		return ResumeOutput{}, preserveFailure(err)
	}
	return output, nil
}

func validPurchase(value map[string]any) bool {
	drop, ok := value["dropId"].(string)
	if !ok || strings.TrimSpace(drop) == "" {
		return false
	}
	option, ok := value["optionId"].(string)
	if !ok || strings.TrimSpace(option) == "" {
		return false
	}
	quantity, ok := value["quantity"].(float64)
	return ok && quantity >= 1 && quantity == float64(int(quantity))
}
