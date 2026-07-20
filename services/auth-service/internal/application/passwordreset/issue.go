package passwordreset

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/google/uuid"
)

func (s *Service) Issue(ctx context.Context, input IssueInput) (IssueOutput, error) {
	resetID, err := uuid.Parse(input.ResetID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return IssueOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "재설정 Challenge 요청이 올바르지 않습니다.")
	}
	method := domainidentity.Type(strings.TrimSpace(input.Method))
	if method != domainidentity.TypeEmail && method != domainidentity.TypePhone {
		return IssueOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "확인 수단이 올바르지 않습니다.")
	}

	var output IssueOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		reset, findErr := repositories.Resets.FindForUpdate(ctx, resetID)
		if errors.Is(findErr, domainpasswordreset.ErrNotFound) {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		if reset.IntentID == nil {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if _, verifyErr := s.verifyIntent(ctx, repositories, *reset.IntentID, input.OwnerProof, input.CSRFToken, true); verifyErr != nil {
			return verifyErr
		}
		now := s.clock.Now().UTC()
		if reset.Status != domainpasswordreset.StatusRequested || !reset.ExpiresAt.After(now) {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}

		challengeID := uuid.New()
		expiresAt := minTime(now.Add(s.challengeTTL()), reset.ExpiresAt)
		code, codeErr := s.cryptography.VerificationCode()
		if codeErr != nil {
			return unavailable(codeErr)
		}
		var destination, masked string
		var identityID *uuid.UUID
		channel := domainchallenge.ChannelEmailCode
		if reset.IdentityID != nil {
			target, identityErr := repositories.Identities.FindByIDForUpdate(ctx, *reset.IdentityID)
			if identityErr != nil {
				return unavailable(identityErr)
			}
			if target.Type != method {
				return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "선택한 확인 수단을 사용할 수 없습니다.")
			}
			destination, masked = target.NormalizedValue, target.MaskedValue
			identityID = &target.ID
			if method == domainidentity.TypePhone {
				channel = domainchallenge.ChannelSMSCode
			}
		} else {
			destination, masked = "decoy:"+challengeID.String(), "***"
			if method == domainidentity.TypePhone {
				channel = domainchallenge.ChannelSMSCode
			}
		}
		verification, challengeErr := domainchallenge.New(domainchallenge.NewInput{
			ID: challengeID, SubjectType: domainchallenge.SubjectPasswordReset, SubjectID: resetID,
			Purpose: domainchallenge.PurposePasswordReset, Method: domainchallenge.Method(method), Channel: channel,
			Destination: destination, DestinationLookupHash: s.cryptography.Hash("destination", destination),
			IdentityID: identityID, CodeHash: s.cryptography.Hash("challenge", challengeID.String(), code),
			VerifierKeyVersion: 1, MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute),
			ExpiresAt: expiresAt, CreatedAt: now,
		})
		if challengeErr != nil {
			return unavailable(challengeErr)
		}
		if issueErr := repositories.Challenges.Issue(ctx, verification); issueErr != nil {
			return unavailable(issueErr)
		}
		if attachErr := reset.AttachChallenge(challengeID); attachErr != nil {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if saveErr := repositories.Resets.Save(ctx, &reset); saveErr != nil {
			return unavailable(saveErr)
		}
		if reset.IdentityID != nil {
			ciphertext, sealErr := s.cryptography.Seal(map[string]string{"code": code, "destination": destination})
			if sealErr != nil {
				return unavailable(sealErr)
			}
			deliveryID := uuid.New()
			if storeErr := repositories.Challenges.StoreDeliveryPayload(ctx, domainchallenge.DeliveryPayload{
				ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: ciphertext,
				KeyID: "auth-replay-v1", AADHash: s.cryptography.Hash("delivery", challengeID.String()), ExpiresAt: expiresAt,
			}); storeErr != nil {
				return unavailable(storeErr)
			}
			if appendErr := repositories.Outbox.Append(ctx, domainoutbox.Event{
				ID: uuid.New(), Type: "Auth.PasswordResetVerificationRequested", AggregateType: "PasswordReset",
				AggregateID: resetID, Version: reset.Version,
				Payload:       eventPayload(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String()}),
				CorrelationID: *reset.IntentID,
			}); appendErr != nil {
				return unavailable(appendErr)
			}
			if s.config.VirtualAdapterEnabled {
				virtualCiphertext, virtualErr := s.cryptography.SealVirtual(map[string]string{"code": code})
				if virtualErr != nil {
					return unavailable(virtualErr)
				}
				if storeErr := repositories.Challenges.StoreVirtualProjection(ctx, domainchallenge.VirtualProjection{
					ChallengeID: challengeID, Channel: channel, ChallengeVersion: verification.Version,
					CodeCiphertext: virtualCiphertext, CodeKeyID: "auth-virtual-v1", MaskedDestination: masked,
					Status: domainchallenge.VirtualReady, ExpiresAt: expiresAt, CreatedAt: now,
				}); storeErr != nil {
					return unavailable(storeErr)
				}
			}
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.password_reset.challenge_issued", "authentication_intent", *reset.IntentID, resetID,
			map[string]string{"method": string(method)}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		output = IssueOutput{ChallengeID: challengeID.String(), ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return IssueOutput{}, preserveFailure(err)
	}
	return output, nil
}
