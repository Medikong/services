package operator

import (
	"context"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type Service struct {
	transactor Transactor
	crypto     Cryptography
	approvals  ApprovalPort
	decisions  AuthorizationDecisionPort
	strongTTL  time.Duration
	clock      Clock
}

type Config struct {
	StrongAuthTTL time.Duration
}

func NewService(transactor Transactor, crypto Cryptography, config Config, approvals ApprovalPort, decisions AuthorizationDecisionPort, clocks ...Clock) *Service {
	if approvals == nil {
		approvals = DenyApprovalPort{}
	}
	if decisions == nil {
		decisions = DenyAuthorizationDecisionPort{}
	}
	if config.StrongAuthTTL <= 0 {
		config.StrongAuthTTL = 5 * time.Minute
	}
	clock := Clock(wallClock{})
	if len(clocks) > 0 && clocks[0] != nil {
		clock = clocks[0]
	}
	return &Service{transactor: transactor, crypto: crypto, approvals: approvals, decisions: decisions, strongTTL: config.StrongAuthTTL, clock: clock}
}

func (s *Service) authorize(ctx context.Context, principal domainsession.Principal, decision string, reauthRequired bool, action, resource string) error {
	if !principal.Authenticated || principal.UserID == uuid.Nil || principal.SessionID == uuid.Nil {
		return failure.Forbidden("AUTH_FORBIDDEN", "유효한 운영자 Session이 필요합니다.")
	}
	if !strongSession(principal, s.strongTTL, s.clock.Now().UTC()) {
		if reauthRequired {
			return failure.Forbidden("AUTH_REAUTH_REQUIRED", "최근 강한 인증이 필요합니다.")
		}
		return failure.Forbidden("AUTH_FORBIDDEN", "최근 강한 인증이 필요합니다.")
	}
	if strings.TrimSpace(decision) == "" {
		return failure.Forbidden("AUTH_FORBIDDEN", "외부 인가 결정을 확인할 수 없습니다.")
	}
	if err := s.decisions.Verify(ctx, decision, action, principal.UserID.String(), resource); err != nil {
		return failure.Forbidden("AUTH_FORBIDDEN", "외부 인가 결정을 확인할 수 없습니다.")
	}
	return nil
}

func strongSession(principal domainsession.Principal, ttl time.Duration, now time.Time) bool {
	if principal.Method != "email_password" && principal.Method != "passkey" {
		return false
	}
	if principal.AuthenticatedAt.IsZero() || principal.AuthenticatedAt.After(now) {
		return false
	}
	return now.Sub(principal.AuthenticatedAt) <= ttl
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }
