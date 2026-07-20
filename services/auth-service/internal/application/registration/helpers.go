package registration

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

func registrationVerifiedMethods(registration domainregistration.Registration) []string {
	methods := make([]string, len(registration.VerifiedMethods))
	for index, method := range registration.VerifiedMethods {
		methods[index] = string(method)
	}
	return methods
}

func startOutput(registration domainregistration.Registration, token string) StartOutput {
	return StartOutput{
		RegistrationID: registration.ID.String(), Status: registration.Status,
		RequiredVerifications: []string{"email", "phone"}, VerifiedMethods: registrationVerifiedMethods(registration),
		ExpiresAt: registration.ExpiresAt, RegistrationStatusToken: token, StatusTokenExpiresAt: registration.StatusTokenExpires,
	}
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func stableIdempotency(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}

func eventPayload(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func registrationRetryable(status domainregistration.Status) bool {
	return status == domainregistration.StatusPendingVerification || status == domainregistration.StatusAwaitingUserLink || status == domainregistration.StatusLinked || status == domainregistration.StatusIssuingSession
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 254 || strings.Count(value, "@") != 1 || strings.HasPrefix(value, "@") || strings.HasSuffix(value, "@") {
		return "", errors.New("invalid email")
	}
	return value, nil
}

func normalizePhone(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	value = strings.ReplaceAll(value, "-", "")
	if len(value) < 8 || len(value) > 20 || !strings.HasPrefix(value, "+") {
		return "", errors.New("invalid phone")
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return "", errors.New("invalid phone")
		}
	}
	return value, nil
}

func maskEmail(value string) string {
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	if len(local) <= 1 {
		local = "*"
	} else {
		local = local[:1] + "***"
	}
	return local + "@" + parts[1]
}

func maskPhone(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:3] + strings.Repeat("*", len(value)-5) + value[len(value)-2:]
}

func (s *Service) registrationTTL() time.Duration {
	if s.config.RegistrationTTL > 0 {
		return s.config.RegistrationTTL
	}
	return 30 * time.Minute
}

func (s *Service) statusRetention() time.Duration {
	if s.config.StatusTokenRetention > 0 {
		return s.config.StatusTokenRetention
	}
	return 30 * time.Minute
}

func (s *Service) challengeTTL() time.Duration {
	if s.config.ChallengeTTL > 0 {
		return s.config.ChallengeTTL
	}
	return 10 * time.Minute
}

func (s *Service) resendDelay() time.Duration {
	if s.config.ChallengeResendDelay > 0 {
		return s.config.ChallengeResendDelay
	}
	return time.Minute
}

func (s *Service) linkWindow() time.Duration {
	if s.config.LinkAcceptanceWindow > 0 {
		return s.config.LinkAcceptanceWindow
	}
	return 10 * time.Minute
}

func (s *Service) sessionWindow() time.Duration {
	if s.config.SessionDeliveryWindow > 0 {
		return s.config.SessionDeliveryWindow
	}
	return 10 * time.Minute
}
