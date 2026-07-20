package identity

import (
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/google/uuid"
)

const defaultResendDelay = time.Minute

func (s *Service) linkTTL() time.Duration {
	if s.config.LinkTTL > 0 {
		return s.config.LinkTTL
	}
	return 10 * time.Minute
}

func (s *Service) challengeTTL() time.Duration {
	return s.linkTTL()
}

func (s *Service) recoveryTTL() time.Duration {
	if s.config.RecoveryTTL > 0 {
		return s.config.RecoveryTTL
	}
	return 5 * time.Minute
}

func minTime(first, second time.Time) time.Time {
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

func validIdempotencyKey(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func mapIdentityError(err error) error {
	if errors.Is(err, domainidentity.ErrConflict) {
		return failure.Conflict("AUTH_IDENTITY_LINK_CONFLICT", "이미 사용할 수 없는 휴대폰 인증 수단입니다.")
	}
	return unavailable(err)
}
