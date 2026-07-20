package passwordreset

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) Complete(ctx context.Context, input CompleteInput) error {
	resetID, err := uuid.Parse(input.ResetID)
	if err != nil || input.NewPassword != input.ConfirmPassword || strings.TrimSpace(input.IdempotencyKey) == "" {
		return failure.Invalid("AUTH_INPUT_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err := (domainidentity.PasswordPolicy{MinimumLength: s.config.PasswordMinLength}).Validate(input.NewPassword); err != nil {
		return failure.Invalid("AUTH_PASSWORD_POLICY_NOT_MET", "비밀번호 정책을 만족하지 않습니다.")
	}

	var fence domainsession.RevocationFence
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		reset, findErr := repositories.Resets.FindForUpdate(ctx, resetID)
		if errors.Is(findErr, domainpasswordreset.ErrNotFound) {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		if reset.IntentID == nil || reset.IdentityID == nil {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if _, verifyErr := s.verifyIntent(ctx, repositories, *reset.IntentID, input.OwnerProof, input.CSRFToken, true); verifyErr != nil {
			return verifyErr
		}
		now := s.clock.Now().UTC()
		if reset.Status != domainpasswordreset.StatusChallengeVerified || !reset.ExpiresAt.After(now) {
			return failure.New(failure.KindConflict, "AUTH_PASSWORD_RESET_GRANT_EXPIRED", "비밀번호 재설정 권한이 만료되었습니다.")
		}
		if input.Channel != "web" && !s.cryptography.Equal(reset.ResetGrantHash, resetID.String(), input.ResetGrant) {
			return failure.New(failure.KindConflict, "AUTH_PASSWORD_RESET_GRANT_EXPIRED", "비밀번호 재설정 권한이 만료되었습니다.")
		}
		passwordHash, hashErr := s.cryptography.HashPassword(input.NewPassword)
		if hashErr != nil {
			return unavailable(hashErr)
		}
		if replaceErr := repositories.Identities.ReplacePasswordCredential(ctx, *reset.IdentityID, passwordHash); replaceErr != nil {
			return unavailable(replaceErr)
		}
		link, linkErr := repositories.Identities.FindActiveLinkForIdentity(ctx, *reset.IdentityID)
		if linkErr != nil {
			return unavailable(linkErr)
		}
		if completeErr := reset.Complete(now); completeErr != nil {
			return failure.New(failure.KindConflict, "AUTH_PASSWORD_RESET_GRANT_EXPIRED", "비밀번호 재설정 권한이 만료되었습니다.")
		}
		if saveErr := repositories.Resets.Save(ctx, &reset); saveErr != nil {
			return unavailable(saveErr)
		}
		if appendErr := repositories.Outbox.Append(ctx, domainoutbox.Event{
			ID: uuid.New(), Type: "Auth.PasswordResetCompleted", AggregateType: "PasswordReset",
			AggregateID: resetID, Version: reset.Version,
			Payload: eventPayload(map[string]string{"passwordResetId": resetID.String()}), CorrelationID: *reset.IntentID,
		}); appendErr != nil {
			return unavailable(appendErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.password_reset.completed", "authentication_intent", *reset.IntentID, resetID,
			map[string]string{"status": "completed"}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		if repositories.Sessions == nil {
			return unavailable(nil)
		}
		if s.revocations != nil {
			targets, findErr := repositories.Sessions.FindActiveForUserForUpdate(ctx, link.UserID)
			if findErr != nil {
				return unavailable(findErr)
			}
			if len(targets) > 0 {
				var fenceErr error
				fence, fenceErr = s.revocations.Fence(ctx, targets)
				if fenceErr != nil {
					return unavailable(fenceErr)
				}
			}
		}
		if revokeErr := repositories.Sessions.RevokeForUser(ctx, link.UserID, "password_reset"); revokeErr != nil {
			return unavailable(revokeErr)
		}
		return nil
	})
	if fence != nil {
		if resolveErr := fence.Resolve(context.WithoutCancel(ctx)); resolveErr != nil {
			return unavailable(resolveErr)
		}
	}
	if err != nil {
		return preserveFailure(err)
	}
	return nil
}
