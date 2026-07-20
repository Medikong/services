package registration

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

func (s *Service) IssueChallenge(ctx context.Context, input IssueChallengeInput) (IssueChallengeOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return IssueChallengeOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "가입 Challenge 요청이 올바르지 않습니다.")
	}
	method := domainregistration.Method(strings.ToLower(strings.TrimSpace(input.Method)))
	if method != domainregistration.MethodEmail && method != domainregistration.MethodPhone {
		return IssueChallengeOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "확인 수단이 올바르지 않습니다.")
	}

	var output IssueChallengeOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		registration, findErr := repositories.Registrations.FindForUpdate(ctx, registrationID)
		if errors.Is(findErr, domainregistration.ErrNotFound) {
			return failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		if _, verifyErr := s.verifyActiveIntent(ctx, repositories, registration.IntentID, input.OwnerProof, input.CSRFToken, true); verifyErr != nil {
			return verifyErr
		}
		now := s.clock.Now().UTC()
		if registration.Status != domainregistration.StatusPendingVerification || !registration.ExpiresAt.After(now) {
			return failure.New(failure.KindConflict, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청 시간이 만료되었습니다.")
		}

		identityID := registration.EmailIdentityID
		purpose, channel := domainchallenge.PurposeSignupEmail, domainchallenge.ChannelEmailCode
		if method == domainregistration.MethodPhone {
			identityID, purpose, channel = registration.PhoneIdentityID, domainchallenge.PurposeSignupPhone, domainchallenge.ChannelSMSCode
		}
		target, identityErr := repositories.Identities.FindByIDForUpdate(ctx, identityID)
		if identityErr != nil {
			return unavailable(identityErr)
		}
		code, codeErr := s.cryptography.VerificationCode()
		if codeErr != nil {
			return unavailable(codeErr)
		}
		challengeID := uuid.New()
		verification, newErr := domainchallenge.New(domainchallenge.NewInput{
			ID: challengeID, SubjectType: domainchallenge.SubjectRegistration, SubjectID: registrationID, Purpose: purpose,
			Method: domainchallenge.Method(method), Channel: channel, Destination: target.NormalizedValue,
			DestinationLookupHash: s.cryptography.Hash("destination", target.NormalizedValue), IdentityID: &identityID,
			CodeHash: s.cryptography.Hash("challenge", challengeID.String(), code), VerifierKeyVersion: 1,
			MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(s.resendDelay()),
			ExpiresAt: minTime(now.Add(s.challengeTTL()), registration.ExpiresAt), CreatedAt: now,
		})
		if newErr != nil {
			return unavailable(newErr)
		}
		if issueErr := repositories.Challenges.Issue(ctx, verification); issueErr != nil {
			return unavailable(issueErr)
		}
		if attachErr := registration.AttachChallenge(method, challengeID); attachErr != nil {
			return failure.Conflict("AUTH_METHOD_UNAVAILABLE", "현재 상태에서는 해당 확인 수단을 사용할 수 없습니다.")
		}
		if saveErr := repositories.Registrations.Save(ctx, &registration); saveErr != nil {
			return unavailable(saveErr)
		}
		deliveryCiphertext, sealErr := s.cryptography.Seal(map[string]string{"code": code, "destination": target.NormalizedValue})
		if sealErr != nil {
			return unavailable(sealErr)
		}
		deliveryID := uuid.New()
		if storeErr := repositories.Challenges.StoreDeliveryPayload(ctx, domainchallenge.DeliveryPayload{
			ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: deliveryCiphertext,
			KeyID: "auth-replay-v1", AADHash: s.cryptography.Hash("delivery", challengeID.String()), ExpiresAt: verification.ExpiresAt,
		}); storeErr != nil {
			return unavailable(storeErr)
		}
		if s.config.VirtualAdapterEnabled {
			ciphertext, virtualErr := s.cryptography.SealVirtual(map[string]string{"code": code})
			if virtualErr != nil {
				return unavailable(virtualErr)
			}
			if storeErr := repositories.Challenges.StoreVirtualProjection(ctx, domainchallenge.VirtualProjection{
				ChallengeID: challengeID, Channel: channel, ChallengeVersion: verification.Version, CodeCiphertext: ciphertext,
				CodeKeyID: "auth-virtual-v1", MaskedDestination: target.MaskedValue, Status: domainchallenge.VirtualReady,
				ExpiresAt: verification.ExpiresAt, CreatedAt: now,
			}); storeErr != nil {
				return unavailable(storeErr)
			}
		}
		if appendErr := repositories.Outbox.Append(ctx, domainoutbox.Event{
			ID: uuid.New(), Type: "Auth.VerificationRequested", AggregateType: "Registration", AggregateID: registrationID,
			Version: registration.Version, Payload: eventPayload(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String(), "channel": string(channel)}), CorrelationID: registration.IntentID,
		}); appendErr != nil {
			return unavailable(appendErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.registration.challenge_issued", "authentication_intent", registration.IntentID, registrationID,
			map[string]string{"method": string(method)}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		output = IssueChallengeOutput{
			ChallengeID: challengeID.String(), Method: string(method), MaskedDestination: target.MaskedValue,
			ExpiresAt: verification.ExpiresAt, ResendAvailableAt: verification.NextSendAt,
		}
		return nil
	})
	if err != nil {
		return IssueChallengeOutput{}, preserveFailure(err)
	}
	return output, nil
}

func (s *Service) VerifyChallenge(ctx context.Context, input VerifyChallengeInput) (VerifyChallengeOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return VerifyChallengeOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "확인 코드는 여섯 자리여야 합니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return VerifyChallengeOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}

	var output VerifyChallengeOutput
	var postCommitError error
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		registration, findErr := repositories.Registrations.FindForUpdate(ctx, registrationID)
		if errors.Is(findErr, domainregistration.ErrNotFound) {
			return failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		if _, verifyErr := s.verifyActiveIntent(ctx, repositories, registration.IntentID, input.OwnerProof, input.CSRFToken, true); verifyErr != nil {
			return verifyErr
		}
		verification, result, consumeErr := domainchallenge.Consume(ctx, repositories.Challenges, challengeID, s.clock.Now().UTC(), func(current domainchallenge.Challenge) bool {
			return current.SubjectType == domainchallenge.SubjectRegistration && current.SubjectID == registrationID && s.cryptography.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
		})
		if consumeErr != nil {
			return unavailable(consumeErr)
		}
		method, expectedID := domainregistration.Method(verification.Method), registration.EmailChallengeID
		if method == domainregistration.MethodPhone {
			expectedID = registration.PhoneChallengeID
		}
		if expectedID == nil || *expectedID != challengeID {
			return failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "가입 Challenge를 찾을 수 없습니다.")
		}
		if result.Verified {
			if result.Changed {
				if verification.IdentityID == nil {
					return unavailable(nil)
				}
				if markErr := repositories.Identities.MarkVerified(ctx, *verification.IdentityID); markErr != nil {
					return unavailable(markErr)
				}
				if markErr := registration.MarkMethodVerified(method); markErr != nil {
					return unavailable(markErr)
				}
				if saveErr := repositories.Registrations.Save(ctx, &registration); saveErr != nil {
					return unavailable(saveErr)
				}
				if auditErr := repositories.Audit.Append(ctx, "auth.registration.challenge_verified", "authentication_intent", registration.IntentID, registrationID,
					map[string]string{"method": string(method)}, stableIdempotency(input.IdempotencyKey, "verify-registration", challengeID)); auditErr != nil {
					return unavailable(auditErr)
				}
			}
			output = VerifyChallengeOutput{
				ChallengeID: challengeID.String(), Status: "verified", RegistrationStatus: string(registration.Status),
				VerifiedMethods: registrationVerifiedMethods(registration),
			}
			if registration.MethodVerified(domainregistration.MethodEmail) && registration.MethodVerified(domainregistration.MethodPhone) {
				output.RegistrationStatus = "verified"
			}
			return nil
		}

		switch result.Failure {
		case domainchallenge.ConsumeFailureExpired:
			postCommitError = failure.New(failure.KindConflict, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
		case domainchallenge.ConsumeFailureMismatch, domainchallenge.ConsumeFailureInvalid:
			postCommitError = failure.Invalid("AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
		default:
			postCommitError = failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "현재 Challenge를 다시 사용할 수 없습니다.")
		}
		return nil
	})
	if err != nil {
		return VerifyChallengeOutput{}, preserveFailure(err)
	}
	if postCommitError != nil {
		return VerifyChallengeOutput{}, postCommitError
	}
	if output.RegistrationStatus == "verified" {
		completionProof, signErr := s.proofSigner.SignRegistrationCompletion(registrationID.String(), s.statusRetention())
		if signErr != nil {
			return VerifyChallengeOutput{}, unavailable(signErr)
		}
		output.RegistrationCompletionProof = completionProof
	}
	return output, nil
}
