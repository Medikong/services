package session

import (
	"context"
	"errors"
	"strings"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
)

func (s *Service) Authenticate(ctx context.Context, webCookie, bearer string) (domainsession.Principal, error) {
	if strings.TrimSpace(webCookie) != "" && strings.TrimSpace(bearer) != "" {
		return domainsession.Principal{}, invalid("AUTH_MULTIPLE_CREDENTIALS", "하나의 인증 수단만 제출할 수 있습니다.")
	}
	if strings.TrimSpace(webCookie) == "" && strings.TrimSpace(bearer) == "" {
		return domainsession.Principal{Authenticated: false}, nil
	}
	if strings.TrimSpace(bearer) != "" {
		claims, err := s.cryptography.VerifyAccessToken(bearer)
		if err != nil {
			return domainsession.Principal{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		current, err := s.sessions.FindActive(ctx, claims.SessionID)
		if errors.Is(err, domainsession.ErrNotFound) {
			return domainsession.Principal{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		if err != nil {
			return domainsession.Principal{}, unavailable(err)
		}
		if current.UserID != claims.UserID {
			return domainsession.Principal{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		return principalFromSession(current), nil
	}
	current, _, err := s.sessions.FindByWebSecret(ctx, s.cryptography.Hash(webCookie))
	if errors.Is(err, domainsession.ErrNotFound) {
		return domainsession.Principal{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return domainsession.Principal{}, unavailable(err)
	}
	return principalFromSession(current), nil
}

func principalFromSession(current domainsession.Session) domainsession.Principal {
	return domainsession.Principal{
		Authenticated:   true,
		SessionID:       current.ID,
		UserID:          current.UserID,
		Channel:         string(current.Channel),
		Method:          current.Method,
		AuthenticatedAt: current.AuthenticatedAt,
		ExpiresAt:       current.ExpiresAt,
	}
}

func (s *Service) VerifyWebCSRF(ctx context.Context, webCookie, csrfToken string) error {
	if strings.TrimSpace(webCookie) == "" || strings.TrimSpace(csrfToken) == "" {
		return forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	_, credential, err := s.sessions.FindByWebSecret(ctx, s.cryptography.Hash(webCookie))
	if errors.Is(err, domainsession.ErrNotFound) {
		return unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return unavailable(err)
	}
	if !s.cryptography.Equal(credential.CSRFHash, "csrf", csrfToken) {
		return forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	return nil
}
