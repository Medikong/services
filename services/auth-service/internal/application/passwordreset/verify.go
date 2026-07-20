package passwordreset

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/google/uuid"
)

func (s *Service) Verify(ctx context.Context, input VerifyInput) (VerifyOutput, error) {
	resetID, err := uuid.Parse(input.ResetID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return VerifyOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "확인 코드가 올바르지 않습니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return VerifyOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}

	var output VerifyOutput
	var postCommitFailure error
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
		verification, result, consumeErr := domainchallenge.Consume(ctx, repositories.Challenges, challengeID, s.clock.Now().UTC(), func(current domainchallenge.Challenge) bool {
			return current.SubjectType == domainchallenge.SubjectPasswordReset && current.SubjectID == resetID &&
				s.cryptography.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
		})
		if consumeErr != nil {
			return unavailable(consumeErr)
		}
		if verification.SubjectType != domainchallenge.SubjectPasswordReset || verification.SubjectID != resetID {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if reset.ChallengeID == nil || *reset.ChallengeID != challengeID {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if !result.Verified {
			if result.Failure == domainchallenge.ConsumeFailureExpired {
				postCommitFailure = failure.New(failure.KindConflict, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
			} else {
				postCommitFailure = failure.Invalid("AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
			}
			return nil
		}
		if reset.IdentityID == nil {
			output = VerifyOutput{ResetID: resetID.String(), ExpiresAt: reset.ExpiresAt}
			return nil
		}
		grant, grantErr := s.cryptography.Opaque("rgr_")
		if grantErr != nil {
			return unavailable(grantErr)
		}
		if reset.Status == domainpasswordreset.StatusRequested {
			if verifyErr := reset.Verify(s.cryptography.Hash(resetID.String(), grant), s.clock.Now().UTC()); verifyErr != nil {
				return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
			}
		} else if reset.Status == domainpasswordreset.StatusChallengeVerified {
			reset.ResetGrantHash = s.cryptography.Hash(resetID.String(), grant)
		} else {
			return failure.Invalid("AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
		if saveErr := repositories.Resets.Save(ctx, &reset); saveErr != nil {
			return unavailable(saveErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.password_reset.verified", "authentication_intent", *reset.IntentID, resetID,
			map[string]string{"status": "verified"}, stableKey(input.IdempotencyKey, "verify-reset", challengeID)); auditErr != nil {
			return unavailable(auditErr)
		}
		output = VerifyOutput{ResetID: resetID.String(), ExpiresAt: reset.ExpiresAt}
		if input.Channel != "web" {
			output.ResetGrant = grant
		}
		return nil
	})
	if err != nil {
		return VerifyOutput{}, preserveFailure(err)
	}
	if postCommitFailure != nil {
		return VerifyOutput{}, postCommitFailure
	}
	return output, nil
}
