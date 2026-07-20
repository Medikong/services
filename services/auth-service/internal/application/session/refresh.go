package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
)

type refreshOutcome struct {
	tokens        TokenSet
	failure       error
	revokeSession uuid.UUID
}

func (s *Service) Refresh(ctx context.Context, refreshToken, csrfToken, idempotencyKey string) (TokenSet, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return TokenSet{}, invalid("AUTH_INPUT_INVALID", "refresh token과 Idempotency-Key가 필요합니다.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(idempotencyKey)); err != nil {
		return TokenSet{}, invalid("AUTH_INPUT_INVALID", "Idempotency-Key는 UUID여야 합니다.")
	}

	var outcome refreshOutcome
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		return s.refreshTx(ctx, repositories, refreshToken, csrfToken, idempotencyKey, &outcome)
	})
	if err != nil {
		return TokenSet{}, unavailable(err)
	}
	if outcome.revokeSession != uuid.Nil && s.projection != nil {
		if err := s.projection.RevokeSession(ctx, outcome.revokeSession); err != nil {
			return TokenSet{}, unavailable(err)
		}
	}
	if outcome.failure != nil {
		return TokenSet{}, outcome.failure
	}
	return outcome.tokens, nil
}

func (s *Service) refreshTx(ctx context.Context, repositories TxRepositories, refreshToken, csrfToken, idempotencyKey string, outcome *refreshOutcome) error {
	current, credential, err := repositories.Sessions.FindByRefreshSecretForUpdate(ctx, s.cryptography.Hash(refreshToken))
	if errors.Is(err, domainsession.ErrNotFound) {
		return unauthenticated("AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
	}
	if err != nil {
		return unavailable(err)
	}

	operation := "mobile_refresh"
	if credential.Type == "web_refresh_cookie" {
		operation = "web_refresh"
		if strings.TrimSpace(csrfToken) == "" || !s.cryptography.Equal(credential.CSRFHash, "csrf", csrfToken) {
			return forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
		}
	}
	scopeHash := s.cryptography.Hash(operation, current.ID.String())
	keyHash := s.cryptography.Hash(idempotencyKey)
	requestHash := s.cryptography.Hash(operation, refreshToken)
	record, recordErr := repositories.Idempotency.FindForUpdate(ctx, operation, scopeHash, keyHash)
	if recordErr == nil {
		return s.replayExistingRefresh(ctx, repositories, current, credential, record, operation, refreshToken, idempotencyKey, outcome)
	}
	if !errors.Is(recordErr, domainidempotency.ErrNotFound) {
		return unavailable(recordErr)
	}
	if credential.Status == "rotated_pending_delivery" {
		return unauthenticated("AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
	}
	now := s.clock.Now().UTC()
	if credential.Status != "active" || current.Status != "active" || !credential.ExpiresAt.After(now) {
		if credential.FamilyID != nil {
			if err := repositories.Sessions.MarkReuseDetected(ctx, current.ID, *credential.FamilyID); err != nil {
				return unavailable(err)
			}
		}
		outcome.revokeSession = current.ID
		outcome.failure = unauthenticated("AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
		return nil
	}

	state, err := repositories.UserAuthState.FindForUpdate(ctx, current.UserID)
	if errors.Is(err, domainuserauthstate.ErrNotFound) || (err == nil && state.Status != domainuserauthstate.StatusActive) {
		return forbidden("AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 Session을 갱신할 수 없습니다.")
	}
	if err != nil {
		return unavailable(err)
	}
	rawRefresh, err := s.cryptography.Opaque("rtk_")
	if err != nil {
		return unavailable(err)
	}
	if credential.FamilyID == nil {
		return unavailable(nil)
	}
	nextExpiry := now.Add(s.config.RefreshTTL)
	next := domainsession.Credential{
		ID: uuid.New(), SessionID: current.ID, Type: credential.Type,
		SecretHash: s.cryptography.Hash(rawRefresh), CSRFHash: credential.CSRFHash,
		FamilyID: credential.FamilyID, ExpiresAt: nextExpiry,
	}
	if err := repositories.Sessions.RotateRefresh(ctx, credential, next); err != nil {
		return unavailable(err)
	}
	accessToken, accessExpiresAt, err := s.cryptography.SignAccessToken(current.UserID, current.ID, s.config.AccessTTL)
	if err != nil {
		return unavailable(err)
	}
	result := TokenSet{
		SessionID: current.ID.String(), UserID: current.UserID.String(),
		AccessToken: accessToken, AccessTokenExpiresAt: accessExpiresAt,
		RefreshToken: rawRefresh, RefreshTokenExpiresAt: nextExpiry,
		Channel: string(current.Channel), SessionExpiresAt: current.ExpiresAt,
	}
	ciphertext, err := s.cryptography.SealTokenSet(result)
	if err != nil {
		return unavailable(err)
	}
	replayID := uuid.New()
	retryExpiresAt := minExpiry(now.Add(s.recoveryTTL()), nextExpiry)
	bindingHash := s.cryptography.Hash(operation+"_replay", current.ID.String(), idempotencyKey, refreshToken)
	if err := repositories.Idempotency.CreateReplayPayload(ctx, domainidempotency.ReplayPayload{
		ID: replayID, Kind: operation + "_response", Ciphertext: ciphertext, BindingHash: bindingHash, ExpiresAt: retryExpiresAt,
	}); err != nil {
		return unavailable(err)
	}
	resourceID := current.ID
	if err := repositories.Idempotency.CreateCompleted(ctx, domainidempotency.Record{
		ID: uuid.New(), Operation: operation, ScopeHash: scopeHash, KeyHash: keyHash,
		RequestHash: requestHash, ResourceID: &resourceID, ReplayID: &replayID, ExpiresAt: retryExpiresAt,
	}, "Session", "rotated"); err != nil {
		return unavailable(err)
	}
	if err := repositories.Outbox.Append(ctx, domainoutbox.Event{
		ID: uuid.New(), Type: "Auth.SessionRefreshRotated", AggregateType: "Session", AggregateID: current.ID,
		Version: 0, Payload: json.RawMessage(`{"status":"rotated"}`), CorrelationID: current.ID, OccurredAt: now,
	}); err != nil {
		return unavailable(err)
	}
	if err := repositories.Audit.Append(ctx, "auth.session.refresh_rotated", "user", current.UserID, current.ID, map[string]string{"channel": string(current.Channel)}, idempotencyKey); err != nil {
		return unavailable(err)
	}
	outcome.tokens = result
	return nil
}

func (s *Service) replayExistingRefresh(
	ctx context.Context,
	repositories TxRepositories,
	current domainsession.Session,
	credential domainsession.Credential,
	record domainidempotency.Record,
	operation, refreshToken, idempotencyKey string,
	outcome *refreshOutcome,
) error {
	if !s.cryptography.Equal(record.RequestHash, operation, refreshToken) {
		return conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if current.Status != "active" {
		return unauthenticated("AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
	}
	state, err := repositories.UserAuthState.FindForUpdate(ctx, current.UserID)
	if errors.Is(err, domainuserauthstate.ErrNotFound) || (err == nil && state.Status != domainuserauthstate.StatusActive) {
		if record.ReplayID != nil {
			if err := repositories.Idempotency.DestroyReplayPayload(ctx, *record.ReplayID); err != nil {
				return unavailable(err)
			}
		}
		outcome.failure = forbidden("AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 Session을 갱신할 수 없습니다.")
		return nil
	}
	if err != nil {
		return unavailable(err)
	}
	if credential.Status != "rotated" || record.Status != "completed" || record.ReplayID == nil {
		return unavailable(nil)
	}

	payload, err := repositories.Idempotency.FindReplayPayloadForUpdate(ctx, *record.ReplayID)
	if errors.Is(err, domainidempotency.ErrNotFound) {
		return failure.New(failure.KindConflict, "AUTH_REFRESH_RETRY_EXPIRED", "refresh 재시도 가능 시간이 만료되었습니다.")
	}
	if err != nil {
		return unavailable(err)
	}
	now := s.clock.Now().UTC()
	if payload.Kind != operation+"_response" || !s.cryptography.Equal(payload.BindingHash, operation+"_replay", current.ID.String(), idempotencyKey, refreshToken) || payload.DestroyedAt != nil || !payload.ExpiresAt.After(now) {
		if err := repositories.Idempotency.DestroyReplayPayload(ctx, payload.ID); err != nil {
			return unavailable(err)
		}
		outcome.failure = failure.New(failure.KindConflict, "AUTH_REFRESH_RETRY_EXPIRED", "refresh 재시도 가능 시간이 만료되었습니다.")
		return nil
	}
	result, err := s.cryptography.OpenTokenSet(payload.Ciphertext)
	if err != nil || result.SessionID != current.ID.String() {
		return unavailable(err)
	}
	if err := repositories.Idempotency.RecordReplay(ctx, payload.ID); err != nil {
		return unavailable(err)
	}
	outcome.tokens = result
	return nil
}
