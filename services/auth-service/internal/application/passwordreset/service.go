package passwordreset

type Service struct {
	transactions Transactor
	cryptography Cryptography
	ownership    IntentOwnershipVerifier
	clock        Clock
	config       Config
	projection   StatusProjectionWriter
}

func NewService(transactions Transactor, cryptography Cryptography, ownership IntentOwnershipVerifier, clock Clock, config Config, projections ...StatusProjectionWriter) *Service {
	service := &Service{
		transactions: transactions,
		cryptography: cryptography,
		ownership:    ownership,
		clock:        clock,
		config:       config,
	}
	if len(projections) > 0 {
		service.projection = projections[0]
	}
	return service
}
