package intent

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BootstrapConfig struct {
	IntentTTL time.Duration
}

type BootstrapService struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	config      BootstrapConfig
	intents     Repository
	idempotency idempotency.Repository
}

func NewBootstrapService(pool *pgxpool.Pool, keys security.Keys, config BootstrapConfig, intents Repository, idempotency idempotency.Repository) *BootstrapService {
	return &BootstrapService{pool: pool, keys: keys, config: config, intents: intents, idempotency: idempotency}
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
	channel := Channel(strings.ToLower(strings.TrimSpace(input.Channel)))
	if channel != ChannelWeb && channel != ChannelIOS && channel != ChannelAndroid {
		return CreateOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "지원하지 않는 클라이언트 채널입니다.")
	}
	if !validReturnPath(input.ReturnPath) {
		return CreateOutput{}, domain.Problem(400, "AUTH_REDIRECT_INVALID", "내부 복귀 경로만 사용할 수 있습니다.")
	}
	if input.IntentType != "navigation" && input.IntentType != "purchase" {
		return CreateOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "인증 Intent 유형이 올바르지 않습니다.")
	}
	if input.IntentType == "purchase" && (input.ActionContext == nil || input.ActionContext["dropId"] == nil || input.ActionContext["optionId"] == nil || input.ActionContext["quantity"] == nil) {
		return CreateOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "구매 Intent 정보가 부족합니다.")
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return CreateOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "Idempotency-Key 헤더가 필요합니다.")
	}
	actionContext, err := json.Marshal(input.ActionContext)
	if err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	requestHash := s.keys.Hash(string(actionContext), string(channel), input.ReturnPath, input.IntentType)
	ownerProof, err := s.keys.Opaque("af_")
	if err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	csrfToken, err := s.keys.Opaque("csrf_")
	if err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	scopeHash := s.keys.Hash("create_authentication_intent")
	keyHash := s.keys.Hash(input.IdempotencyKey)
	record, err := s.idempotency.FindForUpdate(ctx, tx, "create_authentication_intent", scopeHash, keyHash)
	if err == nil {
		if !hmac.Equal(record.RequestHash, requestHash) {
			return CreateOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
		if record.ResourceID == nil {
			return CreateOutput{}, domain.Unavailable()
		}
		current, err := s.intents.FindActiveForUpdate(ctx, tx, *record.ResourceID)
		if errors.Is(err, ErrNotFound) {
			return CreateOutput{}, domain.Problem(410, "AUTH_INTENT_EXPIRED", "인증 요청 시간이 만료되었습니다.")
		}
		if err != nil {
			return CreateOutput{}, domain.Unavailable()
		}
		if err := s.intents.RotateOwnerProof(ctx, tx, current.ID, s.keys.Hash(current.ID.String(), ownerProof), s.keys.Hash(current.ID.String(), csrfToken)); err != nil {
			return CreateOutput{}, domain.Unavailable()
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateOutput{}, domain.Unavailable()
		}
		return CreateOutput{IntentID: current.ID.String(), Channel: string(current.Channel), ExpiresAt: current.ExpiresAt, OwnerProof: ownerProof, CSRFToken: csrfToken}, nil
	}
	if !errors.Is(err, idempotency.ErrNotFound) {
		return CreateOutput{}, domain.Unavailable()
	}
	id := uuid.New()
	var actionPayloadID *uuid.UUID
	if input.IntentType == "purchase" {
		ciphertext, err := s.keys.Seal(input.ActionContext)
		if err != nil {
			return CreateOutput{}, domain.Unavailable()
		}
		payloadID := uuid.New()
		if err := s.intents.CreateActionPayload(ctx, tx, ActionPayload{ID: payloadID, ActionName: "purchase", Ciphertext: ciphertext, ExpiresAt: time.Now().UTC().Add(s.config.IntentTTL)}); err != nil {
			return CreateOutput{}, domain.Unavailable()
		}
		actionPayloadID = &payloadID
	}
	expiresAt := time.Now().UTC().Add(s.config.IntentTTL)
	if err := s.intents.Create(ctx, tx, CreateParams{
		ID: id, Channel: channel, ReturnPath: input.ReturnPath, Type: input.IntentType,
		ActionContext: actionContext, OwnerProofHash: s.keys.Hash(id.String(), ownerProof),
		CSRFHash: s.keys.Hash(id.String(), csrfToken), ActionPayloadID: actionPayloadID, ExpiresAt: expiresAt,
	}); err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	if actionPayloadID != nil {
		if err := s.intents.BindActionPayload(ctx, tx, id, *actionPayloadID); err != nil {
			return CreateOutput{}, domain.Unavailable()
		}
	}
	if err := s.idempotency.CreateCompleted(ctx, tx, idempotency.NewRecord(
		"create_authentication_intent", scopeHash, keyHash, requestHash, &id, nil, expiresAt,
	), "AuthenticationIntent", "created"); err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateOutput{}, domain.Unavailable()
	}
	return CreateOutput{IntentID: id.String(), Channel: string(channel), ExpiresAt: expiresAt, OwnerProof: ownerProof, CSRFToken: csrfToken}, nil
}

func validReturnPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.Contains(path, "://") && len(path) <= 1024
}

// VerifyOwnershipTx provides a transaction-bound pre-auth check to other
// application services without leaking credential parsing into repositories.
func (s *BootstrapService) VerifyOwnershipTx(ctx context.Context, tx pgx.Tx, intentID uuid.UUID, ownerProof, csrf string, requireCSRF bool) (Intent, error) {
	current, err := s.intents.FindActiveForUpdate(ctx, tx, intentID)
	if errors.Is(err, ErrNotFound) {
		return Intent{}, domain.Problem(404, "AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return Intent{}, domain.Unavailable()
	}
	return s.verifyOwnership(current, ownerProof, csrf, requireCSRF)
}

func (s *BootstrapService) VerifyCompletionReplayOwnershipTx(ctx context.Context, tx pgx.Tx, intentID, sessionID uuid.UUID, ownerProof, csrf string) (Intent, error) {
	current, err := s.intents.FindCompletionReplayForUpdate(ctx, tx, intentID, sessionID)
	if errors.Is(err, ErrNotFound) {
		return Intent{}, domain.Problem(404, "AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return Intent{}, domain.Unavailable()
	}
	return s.verifyOwnership(current, ownerProof, csrf, true)
}

func (s *BootstrapService) verifyOwnership(current Intent, ownerProof, csrf string, requireCSRF bool) (Intent, error) {
	if !s.keys.Equal(current.OwnerProofHash, current.ID.String(), ownerProof) {
		return Intent{}, domain.Problem(403, "AUTH_CSRF_INVALID", "인증 요청 소유를 확인할 수 없습니다.")
	}
	if requireCSRF && current.Channel == ChannelWeb && !s.keys.Equal(current.CSRFHash, current.ID.String(), csrf) {
		return Intent{}, domain.Problem(403, "AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	return current, nil
}

func (s *BootstrapService) GetMethods(ctx context.Context, intentIDRaw, ownerProof string) (string, error) {
	intentID, err := uuid.Parse(intentIDRaw)
	if err != nil {
		return "", domain.Problem(400, "AUTH_INPUT_INVALID", "인증 Intent 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, err := s.VerifyOwnershipTx(ctx, tx, intentID, ownerProof, "", false)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", domain.Unavailable()
	}
	return string(current.Channel), nil
}
