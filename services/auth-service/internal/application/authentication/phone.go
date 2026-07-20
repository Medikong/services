package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
)

type PhoneService struct {
	transactions Transactor
	ownership    OwnershipVerifier
	cryptography Cryptography
	clock        Clock
	sessions     SessionIssuer
	config       Config
}

func NewPhoneService(transactions Transactor, ownership OwnershipVerifier, cryptography Cryptography, clock Clock, sessions SessionIssuer, config Config) *PhoneService {
	return &PhoneService{transactions: transactions, ownership: ownership, cryptography: cryptography, clock: clock, sessions: sessions, config: config}
}

func (s *PhoneService) Issue(ctx context.Context, input PhoneIssueInput) (PhoneIssueOutput, error) {
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return PhoneIssueOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "휴대폰 로그인 요청이 올바르지 않습니다.")
	}
	phone, err := normalizePhone(input.Phone)
	if err != nil {
		return PhoneIssueOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}

	var output PhoneIssueOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		currentIntent, verifyErr := verifyIntent(ctx, repositories.Intents, s.ownership, intentID, input.OwnerProof, input.CSRFToken, true)
		if verifyErr != nil {
			return verifyErr
		}
		if rememberErr := repositories.Intents.SetRememberMe(ctx, intentID, input.RememberMe); rememberErr != nil {
			return preserveFailure(rememberErr)
		}
		currentIntent.RememberMe = &input.RememberMe

		var identityID *uuid.UUID
		destination, masked := "decoy:"+intentID.String(), "***"
		if currentIdentity, _, findErr := repositories.Identities.FindActivePhoneLinkForUpdate(ctx, phone); findErr == nil {
			identityID = &currentIdentity.ID
			destination, masked = currentIdentity.NormalizedValue, currentIdentity.MaskedValue
		} else if !errors.Is(findErr, domainidentity.ErrNotFound) {
			return preserveFailure(findErr)
		}

		challengeID := uuid.New()
		now := s.clock.Now().UTC()
		expiresAt := minimumTime(now.Add(s.challengeTTL()), currentIntent.ExpiresAt)
		code, codeErr := s.cryptography.VerificationCode()
		if codeErr != nil {
			return preserveFailure(codeErr)
		}
		verification, challengeErr := domainchallenge.New(domainchallenge.NewInput{
			ID: challengeID, SubjectType: domainchallenge.SubjectPhoneSignIn, SubjectID: intentID,
			Purpose: domainchallenge.PurposePhoneSignIn, Method: domainchallenge.MethodPhone, Channel: domainchallenge.ChannelSMSCode,
			Destination: destination, DestinationLookupHash: s.cryptography.Hash("destination", destination), IdentityID: identityID,
			CodeHash: s.cryptography.Hash("challenge", challengeID.String(), code), VerifierKeyVersion: 1,
			MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute), ExpiresAt: expiresAt, CreatedAt: now,
		})
		if challengeErr != nil {
			return preserveFailure(challengeErr)
		}
		if issueErr := repositories.Challenges.Issue(ctx, verification); issueErr != nil {
			return preserveFailure(issueErr)
		}
		if identityID != nil {
			ciphertext, sealErr := s.cryptography.SealDelivery(code, destination)
			if sealErr != nil {
				return preserveFailure(sealErr)
			}
			deliveryID := uuid.New()
			if storeErr := repositories.Challenges.StoreDeliveryPayload(ctx, domainchallenge.DeliveryPayload{
				ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: ciphertext,
				KeyID: "auth-replay-v1", AADHash: s.cryptography.Hash("delivery", challengeID.String()), ExpiresAt: expiresAt,
			}); storeErr != nil {
				return preserveFailure(storeErr)
			}
			payload, marshalErr := json.Marshal(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String()})
			if marshalErr != nil {
				return preserveFailure(marshalErr)
			}
			if appendErr := repositories.Outbox.Append(ctx, domainoutbox.Event{
				ID: uuid.New(), Type: "Auth.PhoneSignInVerificationRequested", AggregateType: "AuthenticationIntent",
				AggregateID: intentID, Payload: payload, CorrelationID: intentID, OccurredAt: now,
			}); appendErr != nil {
				return preserveFailure(appendErr)
			}
			if s.config.VirtualAdapterEnabled {
				virtualCode, virtualErr := s.cryptography.SealVirtualCode(code)
				if virtualErr != nil {
					return preserveFailure(virtualErr)
				}
				if projectionErr := repositories.Challenges.StoreVirtualProjection(ctx, domainchallenge.VirtualProjection{
					ChallengeID: challengeID, Channel: domainchallenge.ChannelSMSCode, ChallengeVersion: verification.Version,
					CodeCiphertext: virtualCode, CodeKeyID: "auth-virtual-v1", MaskedDestination: masked,
					Status: domainchallenge.VirtualReady, ExpiresAt: expiresAt, CreatedAt: now,
				}); projectionErr != nil {
					return preserveFailure(projectionErr)
				}
			}
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.phone_signin.challenge_issued", "authentication_intent", intentID, intentID, map[string]string{"status": "accepted"}, input.IdempotencyKey); auditErr != nil {
			return preserveFailure(auditErr)
		}
		output = PhoneIssueOutput{ChallengeID: challengeID.String(), ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return PhoneIssueOutput{}, preserveFailure(err)
	}
	return output, nil
}

func (s *PhoneService) Verify(ctx context.Context, input PhoneVerifyInput) (Completed, error) {
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return Completed{}, failure.Invalid("AUTH_INPUT_INVALID", "확인 코드가 올바르지 않습니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return Completed{}, failure.Invalid("AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}

	var completed Completed
	var committedFailure error
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		currentIntent, verifyErr := verifyIntent(ctx, repositories.Intents, s.ownership, intentID, input.OwnerProof, input.CSRFToken, true)
		if verifyErr != nil {
			return verifyErr
		}
		verification, result, consumeErr := domainchallenge.Consume(ctx, repositories.Challenges, challengeID, s.clock.Now().UTC(), func(current domainchallenge.Challenge) bool {
			return current.SubjectType == domainchallenge.SubjectPhoneSignIn && current.SubjectID == intentID && s.cryptography.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
		})
		if consumeErr != nil {
			return preserveFailure(consumeErr)
		}
		if verification.SubjectType != domainchallenge.SubjectPhoneSignIn || verification.SubjectID != intentID || verification.IdentityID == nil {
			return failure.Unauthenticated("AUTH_SIGNIN_FAILED", "휴대폰 로그인 정보를 확인할 수 없습니다.")
		}
		if !result.Verified {
			if result.Failure == domainchallenge.ConsumeFailureExpired {
				committedFailure = failure.New(failure.KindConflict, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
			} else {
				committedFailure = failure.Invalid("AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
			}
			return nil
		}
		link, findErr := repositories.Identities.FindActiveLinkForIdentity(ctx, *verification.IdentityID)
		if errors.Is(findErr, domainidentity.ErrNotFound) {
			return failure.Conflict("AUTH_PHONE_IDENTITY_NOT_LINKED", "연결된 휴대폰 인증 수단이 없습니다.")
		}
		if findErr != nil {
			return preserveFailure(findErr)
		}
		issued, issueErr := s.sessions.IssueTx(ctx, repositories.Session, applicationsession.IssueInput{
			UserID: link.UserID, IdentityID: *verification.IdentityID, IdentityLink: link.ID,
			Method: "phone_otp", Channel: string(currentIntent.Channel),
			RememberMe: currentIntent.RememberMe != nil && *currentIntent.RememberMe, WebCSRFToken: input.CSRFToken,
		})
		if issueErr != nil {
			return issueErr
		}
		sessionID, parseErr := uuid.Parse(issued.SessionID)
		if parseErr != nil {
			return preserveFailure(parseErr)
		}
		if consumeIntentErr := repositories.Intents.Consume(ctx, intentID, sessionID, "session_issued"); consumeIntentErr != nil {
			return preserveFailure(consumeIntentErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.phone_signin.completed", "authentication_intent", intentID, intentID, map[string]string{"status": "completed"}, stableKey(input.IdempotencyKey, "phone-signin", challengeID)); auditErr != nil {
			return preserveFailure(auditErr)
		}
		completed = Completed{Issued: issued, NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String()}
		return nil
	})
	if err != nil {
		return Completed{}, preserveFailure(err)
	}
	if committedFailure != nil {
		return Completed{}, committedFailure
	}
	return completed, nil
}

func (s *PhoneService) challengeTTL() time.Duration {
	if s.config.ChallengeTTL > 0 {
		return s.config.ChallengeTTL
	}
	return 10 * time.Minute
}

func minimumTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

func stableKey(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}

func normalizePhone(value string) (string, error) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "-", "")
	if !strings.HasPrefix(value, "+") || len(value) < 8 {
		return "", errors.New("phone")
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return "", errors.New("phone")
		}
	}
	return value, nil
}
