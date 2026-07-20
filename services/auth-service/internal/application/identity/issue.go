package identity

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
)

func (s *Service) IssueIdentityLink(ctx context.Context, input IssueLinkInput) (IssueLinkOutput, error) {
	return s.issueLink(ctx, input, domainchallenge.PurposeIdentityLink, domainchallenge.SubjectIdentityLink)
}

func (s *Service) IssuePhoneReplacement(ctx context.Context, input IssueLinkInput) (IssueLinkOutput, error) {
	return s.issueLink(ctx, input, domainchallenge.PurposePhoneChange, domainchallenge.SubjectPhoneChange)
}

func (s *Service) issueLink(ctx context.Context, input IssueLinkInput, purpose domainchallenge.Purpose, subjectType domainchallenge.SubjectType) (IssueLinkOutput, error) {
	linkID, err := uuid.Parse(input.LinkID)
	if err != nil {
		return IssueLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "인증 수단 연동 식별자가 올바르지 않습니다.")
	}
	var output IssueLinkOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		link, target, err := repositories.Identities.RequestedLinkForUpdate(ctx, linkID)
		if errors.Is(err, domainidentity.ErrNotFound) || (err == nil && link.UserID != input.Principal.UserID) {
			return failure.NotFound("AUTH_IDENTITY_LINK_NOT_FOUND", "인증 수단 연동 요청을 찾을 수 없습니다.")
		}
		if err != nil {
			return unavailable(err)
		}
		now := s.clock.Now().UTC()
		if link.ExpiresAt == nil || !link.ExpiresAt.After(now) {
			return failure.Conflict("AUTH_IDENTITY_LINK_INTENT_EXPIRED", "인증 수단 연동 요청 시간이 만료되었습니다.")
		}
		challengeID := uuid.New()
		code, err := s.cryptography.VerificationCode()
		if err != nil {
			return unavailable(err)
		}
		expiresAt := minTime(now.Add(s.challengeTTL()), *link.ExpiresAt)
		verification, err := domainchallenge.New(domainchallenge.NewInput{
			ID: challengeID, SubjectType: subjectType, SubjectID: linkID, Purpose: purpose,
			Method: domainchallenge.MethodPhone, Channel: domainchallenge.ChannelSMSCode,
			Destination:           target.NormalizedValue,
			DestinationLookupHash: s.cryptography.Hash("destination", target.NormalizedValue),
			IdentityID:            &target.ID,
			CodeHash:              s.cryptography.Hash("challenge", challengeID.String(), code),
			VerifierKeyVersion:    1, MaxAttempts: 5, MaxSends: 5,
			NextSendAt: now.Add(defaultResendDelay), ExpiresAt: expiresAt, CreatedAt: now,
		})
		if err != nil {
			return unavailable(err)
		}
		if err := repositories.Challenges.Issue(ctx, verification); err != nil {
			return unavailable(err)
		}
		if err := repositories.Identities.AttachProofChallenge(ctx, linkID, challengeID); err != nil {
			return unavailable(err)
		}
		ciphertext, err := s.cryptography.SealDelivery(code, target.NormalizedValue)
		if err != nil {
			return unavailable(err)
		}
		deliveryID := uuid.New()
		if err := repositories.Challenges.StoreDeliveryPayload(ctx, domainchallenge.DeliveryPayload{
			ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: ciphertext,
			KeyID: "auth-replay-v1", AADHash: s.cryptography.Hash("delivery", challengeID.String()), ExpiresAt: expiresAt,
		}); err != nil {
			return unavailable(err)
		}
		payload, err := json.Marshal(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String()})
		if err != nil {
			return unavailable(err)
		}
		if err := repositories.Outbox.Append(ctx, domainoutbox.Event{
			ID: uuid.New(), Type: "Auth.IdentityLinkVerificationRequested", AggregateType: "IdentityLink",
			AggregateID: linkID, Version: 0, Payload: payload, CorrelationID: input.Principal.SessionID, OccurredAt: now,
		}); err != nil {
			return unavailable(err)
		}
		if s.config.Virtual {
			encrypted, err := s.cryptography.SealVirtualCode(code)
			if err != nil {
				return unavailable(err)
			}
			if err := repositories.Challenges.StoreVirtualProjection(ctx, domainchallenge.VirtualProjection{
				ChallengeID: challengeID, Channel: domainchallenge.ChannelSMSCode, ChallengeVersion: verification.Version,
				CodeCiphertext: encrypted, CodeKeyID: "auth-virtual-v1", MaskedDestination: target.MaskedValue,
				Status: domainchallenge.VirtualReady, ExpiresAt: expiresAt, CreatedAt: now,
			}); err != nil {
				return unavailable(err)
			}
		}
		if err := repositories.Audit.Append(ctx, "auth.identity_link.challenge_issued", "user", input.Principal.UserID, linkID, map[string]string{"purpose": string(purpose)}, input.IdempotencyKey); err != nil {
			return unavailable(err)
		}
		output = IssueLinkOutput{ChallengeID: challengeID.String(), Masked: target.MaskedValue, ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return IssueLinkOutput{}, unavailable(err)
	}
	return output, nil
}
