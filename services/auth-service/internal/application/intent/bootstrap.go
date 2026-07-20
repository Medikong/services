package intent

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

type BootstrapConfig struct {
	IntentTTL time.Duration
}

type BootstrapService struct {
	transactor Transactor
	crypto     BootstrapCryptography
	clock      Clock
	config     BootstrapConfig
}

func NewBootstrapService(transactor Transactor, crypto BootstrapCryptography, clock Clock, config BootstrapConfig) *BootstrapService {
	return &BootstrapService{transactor: transactor, crypto: crypto, clock: clock, config: config}
}

type CreateInput struct {
	Channel        string
	ReturnPath     string
	IntentType     string
	ActionContext  map[string]any
	IdempotencyKey string
}

type CreateOutput struct {
	IntentID   string
	Channel    string
	ExpiresAt  time.Time
	OwnerProof string
	CSRFToken  string
}

func (s *BootstrapService) Create(ctx context.Context, input CreateInput) (CreateOutput, error) {
	channel := domainintent.Channel(strings.ToLower(strings.TrimSpace(input.Channel)))
	if channel != domainintent.ChannelWeb && channel != domainintent.ChannelIOS && channel != domainintent.ChannelAndroid {
		return CreateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "지원하지 않는 클라이언트 채널입니다.")
	}
	if !validReturnPath(input.ReturnPath) {
		return CreateOutput{}, failure.Invalid("AUTH_REDIRECT_INVALID", "내부 복귀 경로만 사용할 수 있습니다.")
	}
	if input.IntentType != "navigation" && input.IntentType != "purchase" {
		return CreateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "인증 Intent 유형이 올바르지 않습니다.")
	}
	if input.IntentType == "purchase" && (input.ActionContext == nil || input.ActionContext["dropId"] == nil || input.ActionContext["optionId"] == nil || input.ActionContext["quantity"] == nil) {
		return CreateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "구매 Intent 정보가 부족합니다.")
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return CreateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Idempotency-Key 헤더가 필요합니다.")
	}
	actionContext, err := json.Marshal(input.ActionContext)
	if err != nil {
		return CreateOutput{}, unavailable(err)
	}
	requestHash := s.crypto.Hash(string(actionContext), string(channel), input.ReturnPath, input.IntentType)
	ownerProof, err := s.crypto.Opaque("af_")
	if err != nil {
		return CreateOutput{}, unavailable(err)
	}
	csrfToken, err := s.crypto.Opaque("csrf_")
	if err != nil {
		return CreateOutput{}, unavailable(err)
	}

	var output CreateOutput
	err = s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		scopeHash := s.crypto.Hash("create_authentication_intent")
		keyHash := s.crypto.Hash(input.IdempotencyKey)
		record, findErr := repositories.Idempotency.FindForUpdate(ctx, "create_authentication_intent", scopeHash, keyHash)
		if findErr == nil {
			if !hmac.Equal(record.RequestHash, requestHash) {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
			}
			if record.ResourceID == nil {
				return failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", unavailableMessage)
			}
			current, findErr := repositories.Intents.FindActiveForUpdate(ctx, *record.ResourceID)
			if errors.Is(findErr, domainintent.ErrNotFound) {
				return failure.New(failure.KindConflict, "AUTH_INTENT_EXPIRED", "인증 요청 시간이 만료되었습니다.")
			}
			if findErr != nil {
				return unavailable(findErr)
			}
			if rotateErr := repositories.Intents.RotateOwnerProof(ctx, current.ID, s.crypto.Hash(current.ID.String(), ownerProof), s.crypto.Hash(current.ID.String(), csrfToken)); rotateErr != nil {
				return unavailable(rotateErr)
			}
			output = CreateOutput{IntentID: current.ID.String(), Channel: string(current.Channel), ExpiresAt: current.ExpiresAt, OwnerProof: ownerProof, CSRFToken: csrfToken}
			return nil
		}
		if !errors.Is(findErr, domainidempotency.ErrNotFound) {
			return unavailable(findErr)
		}

		now := s.clock.Now().UTC()
		id := uuid.New()
		var actionPayloadID *uuid.UUID
		if input.IntentType == "purchase" {
			ciphertext, sealErr := s.crypto.Seal(input.ActionContext)
			if sealErr != nil {
				return unavailable(sealErr)
			}
			payloadID := uuid.New()
			if createErr := repositories.Intents.CreateActionPayload(ctx, domainintent.ActionPayload{ID: payloadID, ActionName: "purchase", Ciphertext: ciphertext, ExpiresAt: now.Add(s.config.IntentTTL)}); createErr != nil {
				return unavailable(createErr)
			}
			actionPayloadID = &payloadID
		}
		expiresAt := now.Add(s.config.IntentTTL)
		if createErr := repositories.Intents.Create(ctx, domainintent.CreateParams{
			ID: id, Channel: channel, ReturnPath: input.ReturnPath, Type: input.IntentType,
			ActionContext: actionContext, OwnerProofHash: s.crypto.Hash(id.String(), ownerProof),
			CSRFHash: s.crypto.Hash(id.String(), csrfToken), ActionPayloadID: actionPayloadID, ExpiresAt: expiresAt,
		}); createErr != nil {
			return unavailable(createErr)
		}
		if actionPayloadID != nil {
			if bindErr := repositories.Intents.BindActionPayload(ctx, id, *actionPayloadID); bindErr != nil {
				return unavailable(bindErr)
			}
		}
		if createErr := repositories.Idempotency.CreateCompleted(ctx, domainidempotency.NewRecord(
			"create_authentication_intent", scopeHash, keyHash, requestHash, &id, nil, expiresAt,
		), "AuthenticationIntent", "created"); createErr != nil {
			return unavailable(createErr)
		}
		output = CreateOutput{IntentID: id.String(), Channel: string(channel), ExpiresAt: expiresAt, OwnerProof: ownerProof, CSRFToken: csrfToken}
		return nil
	})
	if err != nil {
		return CreateOutput{}, preserveFailure(err)
	}
	return output, nil
}

func (s *BootstrapService) VerifyOwnership(current domainintent.Intent, ownerProof, csrf string, requireCSRF bool) (domainintent.Intent, error) {
	if !s.crypto.Equal(current.OwnerProofHash, current.ID.String(), ownerProof) {
		return domainintent.Intent{}, failure.Forbidden("AUTH_CSRF_INVALID", "인증 요청 소유를 확인할 수 없습니다.")
	}
	if requireCSRF && current.Channel == domainintent.ChannelWeb && !s.crypto.Equal(current.CSRFHash, current.ID.String(), csrf) {
		return domainintent.Intent{}, failure.Forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	return current, nil
}

func (s *BootstrapService) GetMethods(ctx context.Context, intentIDRaw, ownerProof string) (string, error) {
	intentID, err := uuid.Parse(intentIDRaw)
	if err != nil {
		return "", failure.Invalid("AUTH_INPUT_INVALID", "인증 Intent 식별자가 올바르지 않습니다.")
	}
	var channel string
	err = s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		current, findErr := repositories.Intents.FindActiveForUpdate(ctx, intentID)
		if errors.Is(findErr, domainintent.ErrNotFound) {
			return failure.NotFound("AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		current, verifyErr := s.VerifyOwnership(current, ownerProof, "", false)
		if verifyErr != nil {
			return verifyErr
		}
		channel = string(current.Channel)
		return nil
	})
	if err != nil {
		return "", preserveFailure(err)
	}
	return channel, nil
}

func validReturnPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.Contains(path, "://") && len(path) <= 1024
}
