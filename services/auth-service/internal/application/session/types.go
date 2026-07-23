package session

import (
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type Config struct {
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	SessionTTL           time.Duration
	RememberMeSessionTTL time.Duration
	RecoveryTTL          time.Duration
}

type TokenSet struct {
	SessionID             string
	UserID                string
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
	Channel               string
	SessionExpiresAt      time.Time
}

type IssueInput struct {
	UserID             uuid.UUID
	IdentityID         uuid.UUID
	IdentityLink       uuid.UUID
	Method             string
	Channel            string
	RememberMe         bool
	WebCSRFToken       string
	AccessTTLOverride  time.Duration
	SessionTTLOverride time.Duration
}

type Issued struct {
	TokenSet
	WebCookie  string
	CSRFToken  string
	ExpiresAt  time.Time
	RememberMe bool
}

type RotationInput struct {
	Principal         domainsession.Principal
	PreviousWebCookie string
	Rebind            *SessionRebind
}

type SessionRebind struct {
	IdentityID   uuid.UUID
	IdentityLink uuid.UUID
	Method       string
}

type Service struct {
	transactions Transactor
	cryptography Cryptography
	clock        Clock
	config       Config
	sessions     domainsession.Repository
	projection   StatusProjectionWriter
	revocations  SessionRevocationFencer
}

func (s *Service) UseSessionRevocation(fencer SessionRevocationFencer) {
	s.revocations = fencer
}

func NewService(transactions Transactor, cryptography Cryptography, clock Clock, config Config, sessions domainsession.Repository, projections ...StatusProjectionWriter) *Service {
	service := &Service{
		transactions: transactions,
		cryptography: cryptography,
		clock:        clock,
		config:       config,
		sessions:     sessions,
	}
	if len(projections) > 0 {
		service.projection = projections[0]
	}
	return service
}
