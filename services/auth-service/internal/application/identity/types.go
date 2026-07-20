package identity

import (
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
)

type RequestContext struct {
	Principal domainsession.Principal
}

type StartLinkInput struct {
	Principal      domainsession.Principal
	Phone          string
	Proof          string
	IdempotencyKey string
}

type StartLinkOutput struct {
	LinkID    string
	Status    string
	ExpiresAt time.Time
	Existing  bool
}

type IssueLinkInput struct {
	Principal      domainsession.Principal
	LinkID         string
	IdempotencyKey string
}

type IssueLinkOutput struct {
	ChallengeID string
	Masked      string
	ExpiresAt   time.Time
}

type CompleteLinkInput struct {
	Principal         domainsession.Principal
	LinkID            string
	ChallengeID       string
	Code              string
	IdempotencyKey    string
	PreviousWebCookie string
}

type CompleteLinkOutput struct {
	LinkID string
	Issued applicationsession.Issued
}

type ReplacementInput struct {
	Principal      domainsession.Principal
	Phone          string
	Proof          string
	IdempotencyKey string
}

type Config struct {
	Virtual     bool
	LinkTTL     time.Duration
	RecoveryTTL time.Duration
}

type Service struct {
	transactions Transactor
	cryptography Cryptography
	clock        Clock
	reauth       ReauthenticationProofConsumer
	sessions     SessionRotator
	revocations  SessionRevocationFencer
	config       Config
}

func NewService(transactions Transactor, cryptography Cryptography, clock Clock, reauth ReauthenticationProofConsumer, sessions SessionRotator, config Config) *Service {
	return &Service{transactions: transactions, cryptography: cryptography, clock: clock, reauth: reauth, sessions: sessions, config: config}
}

func (s *Service) UseSessionRevocation(fencer SessionRevocationFencer) {
	s.revocations = fencer
}
