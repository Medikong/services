package authentication

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EmailService struct {
	pool       *pgxpool.Pool
	bootstrap  *intent.BootstrapService
	identities identity.Repository
	intents    intent.Repository
	sessions   *appsession.Service
}

func NewEmailService(pool *pgxpool.Pool, bootstrap *intent.BootstrapService, identities identity.Repository, intents intent.Repository, sessions *appsession.Service) *EmailService {
	return &EmailService{pool: pool, bootstrap: bootstrap, identities: identities, intents: intents, sessions: sessions}
}

type EmailInput struct {
	IntentID       string
	OwnerProof     string
	CSRFToken      string
	Email          string
	Password       string
	RememberMe     bool
	IdempotencyKey string
}

func (s *EmailService) SignIn(ctx context.Context, input EmailInput) (Completed, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !strings.Contains(email, "@") || input.Password == "" {
		return Completed{}, domain.Problem(400, "AUTH_INPUT_INVALID", "이메일과 비밀번호가 필요합니다.")
	}
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil {
		return Completed{}, domain.Problem(400, "AUTH_INPUT_INVALID", "인증 Intent 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Completed{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	currentIntent, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, intentID, input.OwnerProof, input.CSRFToken, true)
	if err != nil {
		return Completed{}, err
	}
	currentIdentity, link, credential, err := s.identities.FindEmailCredentialForUpdate(ctx, tx, email)
	if errors.Is(err, identity.ErrNotFound) {
		_ = security.VerifyPassword("", input.Password)
		return Completed{}, domain.Problem(401, "AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
	}
	if err != nil {
		return Completed{}, domain.Unavailable()
	}
	if !security.VerifyPassword(credential.Hash, input.Password) {
		return Completed{}, domain.Problem(401, "AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
	}
	if currentIdentity.CredentialState == "locked" {
		return Completed{}, domain.Problem(423, "AUTH_ACCOUNT_LOCKED", "인증 식별자가 잠겨 있습니다.")
	}
	if currentIdentity.CredentialState == "password_reset_required" {
		return Completed{}, domain.Problem(403, "AUTH_PASSWORD_RESET_REQUIRED", "비밀번호 재설정이 필요합니다.")
	}
	issued, err := s.sessions.IssueTx(ctx, tx, appsession.IssueInput{
		UserID: link.UserID, IdentityID: currentIdentity.ID, IdentityLink: link.ID,
		Method: "email_password", Channel: string(currentIntent.Channel), RememberMe: input.RememberMe, WebCSRFToken: input.CSRFToken,
	})
	if err != nil {
		return Completed{}, err
	}
	if err := s.intents.Consume(ctx, tx, currentIntent.ID, uuidMust(issued.SessionID), "session_issued"); err != nil {
		return Completed{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return Completed{}, domain.Unavailable()
	}
	return Completed{Issued: issued, NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String()}, nil
}

func uuidMust(raw string) uuid.UUID {
	value, _ := uuid.Parse(raw)
	return value
}
