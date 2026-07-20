package reauth

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainreauth "github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) Reauthenticate(ctx context.Context, input Input) (Output, error) {
	if !input.Principal.Authenticated || !validPurpose(input.Purpose) || strings.TrimSpace(input.Password) == "" || !validIdempotencyKey(input.IdempotencyKey) {
		return Output{}, failure.Invalid("AUTH_INPUT_INVALID", "재인증 요청이 올바르지 않습니다.")
	}
	var output Output
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		replayedOutput, replayed, err := s.claimOrReplay(ctx, repositories.Idempotency, input)
		if err != nil {
			return err
		}
		if replayed {
			output = replayedOutput
			return nil
		}

		identity, credential, err := repositories.Identities.FindActiveEmailCredentialForUser(ctx, input.Principal.UserID)
		if errors.Is(err, domainidentity.ErrNotFound) {
			return failure.Unauthenticated("AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
		}
		if err != nil {
			return unavailable(err)
		}
		if !s.cryptography.VerifyPassword(credential.Hash, input.Password) {
			return failure.Unauthenticated("AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
		}
		link, err := repositories.Identities.FindActiveLinkForIdentity(ctx, identity.ID)
		if err != nil {
			return unavailable(err)
		}
		if link.UserID != input.Principal.UserID {
			return failure.Unauthenticated("AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
		}
		issued, err := s.sessions.RotateForDeliveryTx(ctx, repositories.Sessions, applicationsession.RotationInput{
			Principal:         input.Principal,
			PreviousWebCookie: input.PreviousWebCookie,
			Rebind:            &applicationsession.SessionRebind{IdentityID: identity.ID, IdentityLink: link.ID, Method: "email_password"},
		})
		if err != nil {
			return err
		}
		proof, err := s.cryptography.Opaque("rap_")
		if err != nil {
			return unavailable(err)
		}
		now := s.clock.Now().UTC()
		expiresAt := minTime(now.Add(s.proofTTL()), issued.ExpiresAt)
		if err := repositories.Proofs.Create(ctx, domainreauth.Proof{
			ID: uuid.New(), Hash: s.cryptography.Hash("reauth", proof), UserID: input.Principal.UserID,
			SessionID: input.Principal.SessionID, IdentityID: &identity.ID, Purpose: input.Purpose,
			ExpiresAt: expiresAt, CreatedAt: now,
		}); err != nil {
			return unavailable(err)
		}
		output = Output{Proof: proof, Purpose: input.Purpose, ExpiresAt: expiresAt, Issued: issued}
		if err := s.storeReplay(ctx, repositories.Idempotency, input, output); err != nil {
			return err
		}
		if err := repositories.Audit.Append(ctx, "auth.reauthentication.completed", "user", input.Principal.UserID, input.Principal.SessionID, map[string]string{"purpose": input.Purpose}, stableKey(input.IdempotencyKey, "reauth", input.Principal.SessionID)); err != nil {
			return unavailable(err)
		}
		return nil
	})
	if err != nil {
		return Output{}, unavailable(err)
	}
	return output, nil
}

func (s *Service) RecoverWebDelivery(ctx context.Context, webCookie, csrfToken, purpose, password, idempotencyKey string) (Output, error) {
	if strings.TrimSpace(webCookie) == "" || strings.TrimSpace(csrfToken) == "" || !validPurpose(purpose) || strings.TrimSpace(password) == "" || !validIdempotencyKey(idempotencyKey) {
		return Output{}, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	var output Output
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
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
		output, err = s.replay(ctx, repositories.Idempotency, current.ID, purpose, password, idempotencyKey)
		return err
	})
	if err != nil {
		return Output{}, unavailable(err)
	}
	return output, nil
}
