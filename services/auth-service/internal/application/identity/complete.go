package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) CompleteIdentityLink(ctx context.Context, input CompleteLinkInput) (CompleteLinkOutput, error) {
	return s.completeLink(ctx, input, domainchallenge.PurposeIdentityLink, domainchallenge.SubjectIdentityLink, false)
}

func (s *Service) CompletePhoneReplacement(ctx context.Context, input CompleteLinkInput) (CompleteLinkOutput, error) {
	return s.completeLink(ctx, input, domainchallenge.PurposePhoneChange, domainchallenge.SubjectPhoneChange, true)
}

func (s *Service) completeLink(ctx context.Context, input CompleteLinkInput, purpose domainchallenge.Purpose, subjectType domainchallenge.SubjectType, replace bool) (CompleteLinkOutput, error) {
	linkID, err := uuid.Parse(input.LinkID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return CompleteLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "인증 수단 연동 요청이 올바르지 않습니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return CompleteLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	var output CompleteLinkOutput
	var committedFailure error
	var fence domainsession.RevocationFence
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		if replace {
			replayedOutput, replayed, err := s.claimOrReplayPhoneReplacement(ctx, repositories.Idempotency, input, linkID, challengeID)
			if err != nil {
				return err
			}
			if replayed {
				output = replayedOutput
				return nil
			}
		}
		link, target, err := repositories.Identities.RequestedLinkForUpdate(ctx, linkID)
		if errors.Is(err, domainidentity.ErrNotFound) || (err == nil && link.UserID != input.Principal.UserID) {
			return failure.NotFound("AUTH_IDENTITY_LINK_NOT_FOUND", "인증 수단 연동 요청을 찾을 수 없습니다.")
		}
		if err != nil {
			return unavailable(err)
		}
		verification, result, err := domainchallenge.Consume(ctx, repositories.Challenges, challengeID, s.clock.Now().UTC(), func(current domainchallenge.Challenge) bool {
			return current.SubjectType == subjectType && current.SubjectID == linkID && current.Purpose == purpose && s.cryptography.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
		})
		if err != nil {
			return unavailable(err)
		}
		if verification.SubjectID != linkID || !result.Verified {
			if result.Failure == domainchallenge.ConsumeFailureExpired {
				committedFailure = failure.Conflict("AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
			} else {
				committedFailure = failure.Invalid("AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
			}
			return nil
		}
		if err := repositories.Identities.MarkVerified(ctx, target.ID); err != nil {
			return unavailable(err)
		}
		if replace {
			if link.PreviousID == nil {
				return unavailable(nil)
			}
			if err := repositories.Identities.ReplacePhoneLink(ctx, *link.PreviousID, link.ID); err != nil {
				return unavailable(err)
			}
		} else if err := repositories.Identities.ActivateLink(ctx, link.ID); err != nil {
			return unavailable(err)
		}
		output = CompleteLinkOutput{LinkID: link.ID.String()}
		if replace {
			issued, err := s.sessions.RotateForDeliveryTx(ctx, repositories.Sessions, applicationsession.RotationInput{
				Principal: input.Principal, PreviousWebCookie: input.PreviousWebCookie,
			})
			if err != nil {
				return err
			}
			output.Issued = issued
			if err := s.storePhoneReplacementReplay(ctx, repositories.Idempotency, input, linkID, challengeID, output); err != nil {
				return err
			}
		}
		if err := repositories.Audit.Append(ctx, "auth.identity_link.completed", "user", input.Principal.UserID, linkID, map[string]string{"purpose": string(purpose)}, stableKey(input.IdempotencyKey, "identity-link", challengeID)); err != nil {
			return unavailable(err)
		}
		if replace {
			if repositories.Revocations == nil {
				return unavailable(nil)
			}
			if s.revocations != nil {
				targets, findErr := repositories.Revocations.FindActiveForIdentityLinkExceptForUpdate(ctx, *link.PreviousID, input.Principal.SessionID)
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
			if revokeErr := repositories.Revocations.RevokeForIdentityLinkExcept(ctx, *link.PreviousID, input.Principal.SessionID, "phone_replaced"); revokeErr != nil {
				return unavailable(revokeErr)
			}
		}
		return nil
	})
	if fence != nil {
		if resolveErr := fence.Resolve(context.WithoutCancel(ctx)); resolveErr != nil {
			return CompleteLinkOutput{}, unavailable(resolveErr)
		}
	}
	if err != nil {
		return CompleteLinkOutput{}, unavailable(err)
	}
	if committedFailure != nil {
		return CompleteLinkOutput{}, committedFailure
	}
	return output, nil
}

// RecoverPhoneReplacementWebDelivery accepts a rotated credential only for
// the exact replacement response that created its successor credential.
func (s *Service) RecoverPhoneReplacementWebDelivery(ctx context.Context, webCookie, csrfToken, linkIDValue, challengeIDValue, code, idempotencyKey string) (CompleteLinkOutput, error) {
	linkID, err := uuid.Parse(linkIDValue)
	if err != nil || strings.TrimSpace(webCookie) == "" || strings.TrimSpace(csrfToken) == "" || len(strings.TrimSpace(code)) != 6 || !validIdempotencyKey(idempotencyKey) {
		return CompleteLinkOutput{}, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	challengeID, err := uuid.Parse(challengeIDValue)
	if err != nil {
		return CompleteLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	var output CompleteLinkOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		current, credential, err := repositories.Sessions.Sessions.FindRecoveryWebSecretForUpdate(ctx, s.cryptography.Hash(webCookie))
		if errors.Is(err, domainsession.ErrNotFound) {
			return failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		if err != nil {
			return unavailable(err)
		}
		if !s.cryptography.Equal(credential.CSRFHash, "csrf", csrfToken) {
			return failure.Forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
		}
		if credential.DeliveryRecoveryExpiresAt == nil || !credential.DeliveryRecoveryExpiresAt.After(s.clock.Now().UTC()) {
			return deliveryExpired()
		}
		output, err = s.replayPhoneReplacement(ctx, repositories.Idempotency, current.ID, linkID, challengeID, code, idempotencyKey)
		return err
	})
	if err != nil {
		return CompleteLinkOutput{}, unavailable(err)
	}
	return output, nil
}
