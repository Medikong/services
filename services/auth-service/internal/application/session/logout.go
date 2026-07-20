package session

import (
	"context"
	"errors"
	"strings"
	"time"

	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) LogoutByWeb(ctx context.Context, webToken, csrfToken, idempotencyKey string) error {
	if strings.TrimSpace(webToken) == "" || strings.TrimSpace(csrfToken) == "" {
		return unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	var revokedSession uuid.UUID
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		current, credential, err := repositories.Sessions.FindByWebSecretForUpdate(ctx, s.cryptography.Hash(webToken))
		if errors.Is(err, domainsession.ErrNotFound) {
			return unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		if err != nil {
			return unavailable(err)
		}
		if !s.cryptography.Equal(credential.CSRFHash, "csrf", csrfToken) {
			return forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
		}
		record, replayed, err := s.claimLogout(ctx, repositories.Idempotency, "logout_web_session", current.ID.String(), webToken, idempotencyKey)
		if err != nil {
			return err
		}
		if !replayed {
			if err := repositories.Sessions.Revoke(ctx, current.ID, "logout"); err != nil {
				return unavailable(err)
			}
			if err := repositories.Idempotency.Complete(ctx, record.ID, "logged_out"); err != nil {
				return unavailable(err)
			}
		}
		revokedSession = current.ID
		return nil
	})
	if err != nil {
		return unavailable(err)
	}
	return s.invalidateSession(ctx, revokedSession)
}

// LogoutByRefresh handles mobile logout, where the refresh token authorizes the operation.
func (s *Service) LogoutByRefresh(ctx context.Context, refreshToken, idempotencyKey string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	var revokedSession uuid.UUID
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		current, credential, err := repositories.Sessions.FindByRefreshSecretForUpdate(ctx, s.cryptography.Hash(refreshToken))
		if errors.Is(err, domainsession.ErrNotFound) {
			return unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		if err != nil {
			return unavailable(err)
		}
		if credential.FamilyID == nil {
			return unavailable(nil)
		}
		record, replayed, err := s.claimLogout(ctx, repositories.Idempotency, "logout_mobile_session", credential.FamilyID.String(), refreshToken, idempotencyKey)
		if err != nil {
			return err
		}
		if !replayed {
			if err := repositories.Sessions.Revoke(ctx, current.ID, "logout"); err != nil {
				return unavailable(err)
			}
			if err := repositories.Idempotency.Complete(ctx, record.ID, "logged_out"); err != nil {
				return unavailable(err)
			}
		}
		revokedSession = current.ID
		return nil
	})
	if err != nil {
		return unavailable(err)
	}
	return s.invalidateSession(ctx, revokedSession)
}

func (s *Service) claimLogout(ctx context.Context, repository IdempotencyRepository, operation, scope, credential, idempotencyKey string) (domainidempotency.Record, bool, error) {
	parsedKey, err := uuid.Parse(strings.TrimSpace(idempotencyKey))
	if err != nil {
		return domainidempotency.Record{}, false, invalid("AUTH_INPUT_INVALID", "멱등성 키가 필요합니다.")
	}
	record, claimed, err := repository.ClaimProcessing(ctx, domainidempotency.Record{
		ID:          uuid.New(),
		Operation:   operation,
		ScopeHash:   s.cryptography.Hash(operation, scope),
		KeyHash:     s.cryptography.Hash(parsedKey.String()),
		RequestHash: s.cryptography.Hash(operation, credential),
		ExpiresAt:   s.clock.Now().UTC().Add(s.logoutIdempotencyTTL()),
	}, "Session")
	if err != nil {
		return domainidempotency.Record{}, false, unavailable(err)
	}
	if claimed {
		return record, false, nil
	}
	if !s.cryptography.Equal(record.RequestHash, operation, credential) {
		return domainidempotency.Record{}, false, conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" {
		return domainidempotency.Record{}, false, unavailable(nil)
	}
	return record, true, nil
}

func (s *Service) invalidateSession(ctx context.Context, sessionID uuid.UUID) error {
	if sessionID == uuid.Nil || s.projection == nil {
		return nil
	}
	if err := s.projection.RevokeSession(ctx, sessionID); err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Service) logoutIdempotencyTTL() time.Duration {
	ttl := s.config.SessionTTL
	for _, candidate := range []time.Duration{s.config.RememberMeSessionTTL, s.config.RefreshTTL} {
		if candidate > ttl {
			ttl = candidate
		}
	}
	if ttl <= 0 {
		return 24 * time.Hour
	}
	return ttl
}
