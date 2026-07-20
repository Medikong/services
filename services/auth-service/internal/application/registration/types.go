package registration

import (
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
)

type Config struct {
	RegistrationTTL       time.Duration
	StatusTokenRetention  time.Duration
	ChallengeTTL          time.Duration
	ChallengeResendDelay  time.Duration
	LinkAcceptanceWindow  time.Duration
	SessionDeliveryWindow time.Duration
	PasswordMinLength     int
	VirtualAdapterEnabled bool
}

type Service struct {
	transactions  Transactor
	cryptography  Cryptography
	passwords     PasswordHasher
	clock         Clock
	config        Config
	intentProof   IntentOwnershipVerifier
	sessions      SessionIssuer
	proofSigner   CompletionProofSigner
	proofVerifier UserCreationProofVerifier
}

func NewService(
	transactions Transactor,
	cryptography Cryptography,
	passwords PasswordHasher,
	clock Clock,
	config Config,
	intentProof IntentOwnershipVerifier,
	sessions SessionIssuer,
	proofSigner CompletionProofSigner,
	proofVerifier UserCreationProofVerifier,
) *Service {
	return &Service{
		transactions: transactions, cryptography: cryptography, passwords: passwords, clock: clock,
		config: config, intentProof: intentProof, sessions: sessions,
		proofSigner: proofSigner, proofVerifier: proofVerifier,
	}
}

type StartInput struct {
	IntentID           string
	OwnerProof         string
	CSRFToken          string
	Email              string
	Password           string
	Phone              string
	ProfileRequestID   string
	AgreementReceiptID string
	RememberMe         bool
	IdempotencyKey     string
}

type StartOutput struct {
	RegistrationID          string
	Status                  domainregistration.Status
	RequiredVerifications   []string
	VerifiedMethods         []string
	ExpiresAt               time.Time
	RegistrationStatusToken string
	StatusTokenExpiresAt    time.Time
}

type IssueChallengeInput struct {
	RegistrationID string
	OwnerProof     string
	CSRFToken      string
	Method         string
	IdempotencyKey string
}

type IssueChallengeOutput struct {
	ChallengeID       string
	Method            string
	MaskedDestination string
	ExpiresAt         time.Time
	ResendAvailableAt time.Time
}

type VerifyChallengeInput struct {
	RegistrationID string
	ChallengeID    string
	OwnerProof     string
	CSRFToken      string
	Code           string
	IdempotencyKey string
}

type VerifyChallengeOutput struct {
	ChallengeID                 string
	Status                      string
	RegistrationStatus          string
	VerifiedMethods             []string
	RegistrationCompletionProof string
}

type StatusInput struct {
	RegistrationID string
	OwnerProof     string
	CSRFToken      string
	StatusToken    string
}

type StatusOutput struct {
	RegistrationID  string
	Status          domainregistration.Status
	VerifiedMethods []string
	Retryable       bool
	ExpiresAt       time.Time
}

type CompleteInput struct {
	RegistrationID    string
	UserID            string
	UserCreationProof string
	OwnerProof        string
	CSRFToken         string
	IdempotencyKey    string
}

type CompleteOutput struct {
	RegistrationID string
	Status         domainregistration.Status
	Issued         applicationsession.Issued
	NextPath       string
	IntentID       string
}
