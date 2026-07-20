package reauth

import (
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
)

type Input struct {
	Principal         domainsession.Principal
	Purpose           string
	Password          string
	IdempotencyKey    string
	PreviousWebCookie string
}

type Output struct {
	Proof     string
	Purpose   string
	ExpiresAt time.Time
	Issued    applicationsession.Issued
}

type Config struct {
	ProofTTL    time.Duration
	RecoveryTTL time.Duration
}

type Service struct {
	transactions Transactor
	cryptography Cryptography
	clock        Clock
	sessions     SessionRotator
	config       Config
}

func NewService(transactions Transactor, cryptography Cryptography, clock Clock, sessions SessionRotator, config Config) *Service {
	return &Service{transactions: transactions, cryptography: cryptography, clock: clock, sessions: sessions, config: config}
}
