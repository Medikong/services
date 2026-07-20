package authentication

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/google/uuid"
)

type EmailService struct {
	transactions Transactor
	ownership    OwnershipVerifier
	cryptography Cryptography
	sessions     SessionIssuer
}

func NewEmailService(transactions Transactor, ownership OwnershipVerifier, cryptography Cryptography, sessions SessionIssuer) *EmailService {
	return &EmailService{transactions: transactions, ownership: ownership, cryptography: cryptography, sessions: sessions}
}

func (s *EmailService) SignIn(ctx context.Context, input EmailInput) (Completed, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !strings.Contains(email, "@") || strings.TrimSpace(input.Password) == "" {
		return Completed{}, failure.Invalid("AUTH_INPUT_INVALID", "이메일과 비밀번호가 필요합니다.")
	}
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil {
		return Completed{}, failure.Invalid("AUTH_INPUT_INVALID", "인증 Intent 식별자가 올바르지 않습니다.")
	}

	var completed Completed
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		currentIntent, verifyErr := verifyIntent(ctx, repositories.Intents, s.ownership, intentID, input.OwnerProof, input.CSRFToken, true)
		if verifyErr != nil {
			return verifyErr
		}
		currentIdentity, link, credential, findErr := repositories.Identities.FindEmailCredentialForUpdate(ctx, email)
		if errors.Is(findErr, domainidentity.ErrNotFound) {
			return failure.Unauthenticated("AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
		}
		if findErr != nil {
			return preserveFailure(findErr)
		}
		switch currentIdentity.CredentialState {
		case "locked":
			return failure.New(failure.KindConflict, "AUTH_ACCOUNT_LOCKED", "인증 식별자가 잠겨 있습니다.")
		case "password_reset_required":
			return failure.Forbidden("AUTH_PASSWORD_RESET_REQUIRED", "비밀번호 재설정이 필요합니다.")
		}
		if !s.cryptography.VerifyPassword(credential.Hash, input.Password) {
			return failure.Unauthenticated("AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
		}
		issued, issueErr := s.sessions.IssueTx(ctx, repositories.Session, applicationsession.IssueInput{
			UserID: link.UserID, IdentityID: currentIdentity.ID, IdentityLink: link.ID,
			Method: "email_password", Channel: string(currentIntent.Channel), RememberMe: input.RememberMe, WebCSRFToken: input.CSRFToken,
		})
		if issueErr != nil {
			return issueErr
		}
		sessionID, parseErr := uuid.Parse(issued.SessionID)
		if parseErr != nil {
			return preserveFailure(parseErr)
		}
		if consumeErr := repositories.Intents.Consume(ctx, currentIntent.ID, sessionID, "session_issued"); consumeErr != nil {
			return preserveFailure(consumeErr)
		}
		completed = Completed{Issued: issued, NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String()}
		return nil
	})
	if err != nil {
		return Completed{}, preserveFailure(err)
	}
	return completed, nil
}
