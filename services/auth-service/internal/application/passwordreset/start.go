package passwordreset

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/google/uuid"
)

func (s *Service) Start(ctx context.Context, input StartInput) (StartOutput, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Idempotency-Key 헤더가 필요합니다.")
	}
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil {
		return StartOutput{}, failure.NotFound("AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	identifierType := domainidentity.Type(strings.TrimSpace(input.IdentifierType))
	value, err := normalizeIdentifier(identifierType, input.Email, input.Phone)
	if err != nil {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "재설정 식별자가 올바르지 않습니다.")
	}

	var output StartOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		currentIntent, verifyErr := s.verifyIntent(ctx, repositories, intentID, input.OwnerProof, input.CSRFToken, true)
		if verifyErr != nil {
			return verifyErr
		}
		scope := s.cryptography.Hash("start_password_reset", intentID.String())
		keyHash := s.cryptography.Hash(input.IdempotencyKey)
		requestHash := s.cryptography.Hash(string(identifierType), value)
		record, findErr := repositories.Idempotency.FindForUpdate(ctx, "start_password_reset", scope, keyHash)
		if findErr == nil {
			if !s.cryptography.EqualHash(record.RequestHash, requestHash) || record.ResourceID == nil {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
			}
			reset, resetErr := repositories.Resets.FindForUpdate(ctx, *record.ResourceID)
			if errors.Is(resetErr, domainpasswordreset.ErrNotFound) {
				return failure.New(failure.KindConflict, "AUTH_INTENT_EXPIRED", "비밀번호 재설정 요청 시간이 만료되었습니다.")
			}
			if resetErr != nil {
				return unavailable(resetErr)
			}
			output = StartOutput{ResetID: reset.ID.String(), ExpiresAt: reset.ExpiresAt}
			return nil
		}
		if !errors.Is(findErr, domainidempotency.ErrNotFound) {
			return unavailable(findErr)
		}

		var identityID *uuid.UUID
		actual, identityErr := repositories.Identities.FindByValueForUpdate(ctx, identifierType, value)
		if identityErr == nil {
			identityID = &actual.ID
		} else if !errors.Is(identityErr, domainidentity.ErrNotFound) {
			return unavailable(identityErr)
		}
		now := s.clock.Now().UTC()
		expiresAt := minTime(now.Add(s.resetTTL()), currentIntent.ExpiresAt)
		resetID := uuid.New()
		reset, newErr := domainpasswordreset.New(resetID, &currentIntent.ID, identityID, expiresAt, now)
		if newErr != nil {
			return unavailable(newErr)
		}
		if createErr := repositories.Resets.Create(ctx, reset); createErr != nil {
			return unavailable(createErr)
		}
		if createErr := repositories.Idempotency.CreateCompleted(ctx, domainidempotency.NewRecord(
			"start_password_reset", scope, keyHash, requestHash, &resetID, nil, expiresAt,
		), "PasswordReset", "accepted"); createErr != nil {
			return unavailable(createErr)
		}
		if appendErr := repositories.Outbox.Append(ctx, domainoutbox.Event{
			ID: uuid.New(), Type: "Auth.PasswordResetRequested", AggregateType: "PasswordReset",
			AggregateID: resetID, Version: 0,
			Payload: eventPayload(map[string]string{"passwordResetId": resetID.String()}), CorrelationID: currentIntent.ID,
		}); appendErr != nil {
			return unavailable(appendErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.password_reset.requested", "authentication_intent", currentIntent.ID, resetID,
			map[string]string{"status": "accepted"}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		output = StartOutput{ResetID: resetID.String(), ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return StartOutput{}, preserveFailure(err)
	}
	return output, nil
}
