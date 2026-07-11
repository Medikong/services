package signin

import appsession "github.com/Medikong/services/services/auth-service/internal/application/session"

// Completed keeps the navigation contract at the sign-in application layer
// while the embedded Session issuance stays owned by the session service.
type Completed struct {
	appsession.Issued
	NextPath string
	IntentID string
}
