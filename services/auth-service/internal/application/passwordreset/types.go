package passwordreset

import "time"

type Config struct {
	ResetTTL              time.Duration
	ChallengeTTL          time.Duration
	PasswordMinLength     int
	VirtualAdapterEnabled bool
}

type StartInput struct {
	IntentID, OwnerProof, CSRFToken, IdentifierType, Email, Phone, IdempotencyKey string
}

type StartOutput struct {
	ResetID   string
	ExpiresAt time.Time
}

type IssueInput struct {
	ResetID, OwnerProof, CSRFToken, Method, IdempotencyKey string
}

type IssueOutput struct {
	ChallengeID string
	ExpiresAt   time.Time
}

type VerifyInput struct {
	ResetID, ChallengeID, OwnerProof, CSRFToken, Code, Channel, IdempotencyKey string
}

type VerifyOutput struct {
	ResetID    string
	ExpiresAt  time.Time
	ResetGrant string
}

type CompleteInput struct {
	ResetID, OwnerProof, CSRFToken, Channel, ResetGrant, NewPassword, ConfirmPassword, IdempotencyKey string
}
