package passwordreset

type Service struct {
	transactions Transactor
	cryptography Cryptography
	ownership    IntentOwnershipVerifier
	clock        Clock
	config       Config
	revocations  SessionRevocationFencer
}

func NewService(transactions Transactor, cryptography Cryptography, ownership IntentOwnershipVerifier, clock Clock, config Config) *Service {
	return &Service{
		transactions: transactions,
		cryptography: cryptography,
		ownership:    ownership,
		clock:        clock,
		config:       config,
	}
}

func (s *Service) UseSessionRevocation(fencer SessionRevocationFencer) {
	s.revocations = fencer
}
