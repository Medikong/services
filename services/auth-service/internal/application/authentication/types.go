package authentication

import (
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
)

type Completed struct {
	applicationsession.Issued
	NextPath string
	IntentID string
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

type PhoneIssueInput struct {
	IntentID       string
	OwnerProof     string
	CSRFToken      string
	Phone          string
	IdempotencyKey string
	RememberMe     bool
}

type PhoneIssueOutput struct {
	ChallengeID string
	ExpiresAt   time.Time
}

type PhoneVerifyInput struct {
	IntentID       string
	ChallengeID    string
	OwnerProof     string
	CSRFToken      string
	Code           string
	IdempotencyKey string
}

type Config struct {
	VirtualAdapterEnabled bool
	ChallengeTTL          time.Duration
}
