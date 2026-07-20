package passwordreset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

func (s *Service) verifyIntent(ctx context.Context, repositories TxRepositories, intentID uuid.UUID, ownerProof, csrf string, requireCSRF bool) (domainintent.Intent, error) {
	current, err := repositories.Intents.FindActiveForUpdate(ctx, intentID)
	if errors.Is(err, domainintent.ErrNotFound) {
		return domainintent.Intent{}, failure.NotFound("AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return domainintent.Intent{}, unavailable(err)
	}
	return s.ownership.VerifyOwnership(current, ownerProof, csrf, requireCSRF)
}

func (s *Service) resetTTL() time.Duration {
	if s.config.ResetTTL > 0 {
		return s.config.ResetTTL
	}
	return 15 * time.Minute
}

func (s *Service) challengeTTL() time.Duration {
	if s.config.ChallengeTTL > 0 {
		return s.config.ChallengeTTL
	}
	return 10 * time.Minute
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func eventPayload(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func stableKey(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}

func normalizeIdentifier(kind domainidentity.Type, email, phone string) (string, error) {
	if kind == domainidentity.TypeEmail {
		value := strings.ToLower(strings.TrimSpace(email))
		if strings.Count(value, "@") != 1 || len(value) < 3 {
			return "", errors.New("email")
		}
		return value, nil
	}
	if kind == domainidentity.TypePhone {
		value := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(phone), " ", ""), "-", "")
		if !strings.HasPrefix(value, "+") || len(value) < 8 {
			return "", errors.New("phone")
		}
		return value, nil
	}
	return "", errors.New("type")
}
