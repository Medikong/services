package providerlink

import (
	"time"
)

type Link struct {
	ProviderLinkID        int64
	AuthAccountID         string
	AuthProvider          string
	ProviderSubject       string
	ProviderEmail         string
	ProviderEmailVerified bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}
