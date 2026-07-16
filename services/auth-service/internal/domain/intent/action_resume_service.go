package intent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ActionResumeService struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	intents     Repository
	idempotency idempotency.Repository
}

func NewActionResumeService(pool *pgxpool.Pool, keys security.Keys, intents Repository, idempotency idempotency.Repository) *ActionResumeService {
	return &ActionResumeService{pool: pool, keys: keys, intents: intents, idempotency: idempotency}
}

type Input struct {
	Principal      appsession.Principal
	IntentID       string
	IdempotencyKey string
}

type Output struct {
	IntentID      string
	Action        string
	ReturnPath    string
	ActionContext map[string]any
}

func (s *ActionResumeService) Resume(ctx context.Context, input Input) (Output, error) {
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return Output{}, domain.Problem(400, "AUTH_INPUT_INVALID", "인증 후 행동 복구 요청이 올바르지 않습니다.")
	}
	if !input.Principal.Authenticated {
		return Output{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "로그인 상태가 필요합니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Output{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	current, payload, err := s.intents.FindConsumedActionForUpdate(ctx, tx, intentID, input.Principal.SessionID)
	if errors.Is(err, ErrNotFound) {
		return Output{}, domain.Problem(410, "AUTH_INTENT_EXPIRED", "인증 Intent를 더 이상 사용할 수 없습니다.")
	}
	if err != nil {
		return Output{}, domain.Unavailable()
	}

	scopeHash := s.keys.Hash("resume_authenticated_action", intentID.String(), input.Principal.SessionID.String())
	keyHash := s.keys.Hash(input.IdempotencyKey)
	requestHash := s.keys.Hash(intentID.String(), input.Principal.SessionID.String())
	record, err := s.idempotency.FindForUpdate(ctx, tx, "resume_authenticated_action", scopeHash, keyHash)
	if err == nil {
		if !bytes.Equal(record.RequestHash, requestHash) {
			return Output{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
	} else if errors.Is(err, idempotency.ErrNotFound) {
		if payload.DeliveredAt != nil {
			return Output{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "이 행동은 이미 전달되었습니다.")
		}
		if err := s.intents.MarkActionDelivered(ctx, tx, payload.ID); err != nil {
			return Output{}, domain.Unavailable()
		}
		newRecord := idempotency.NewRecord("resume_authenticated_action", scopeHash, keyHash, requestHash, &intentID, nil, time.Now().UTC().Add(5*time.Minute))
		if err := s.idempotency.CreateCompleted(ctx, tx, newRecord, "AuthenticationIntent", "delivered"); err != nil {
			return Output{}, domain.Unavailable()
		}
	} else {
		return Output{}, domain.Unavailable()
	}

	var actionContext map[string]any
	if err := s.keys.Open(payload.Ciphertext, &actionContext); err != nil {
		return Output{}, domain.Unavailable()
	}
	if payload.ActionName != "purchase" || !validPurchase(actionContext) {
		return Output{}, domain.Problem(400, "AUTH_INPUT_INVALID", "허용되지 않은 인증 후 행동입니다.")
	}
	if err := domain.AppendAudit(ctx, tx, "auth.action_resume.delivered", "user", input.Principal.UserID, intentID, map[string]string{"action": payload.ActionName}, input.IdempotencyKey); err != nil {
		return Output{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return Output{}, domain.Unavailable()
	}
	return Output{IntentID: intentID.String(), Action: payload.ActionName, ActionContext: actionContext, ReturnPath: current.ReturnPath}, nil
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
