package authentication

import appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"

// Completed keeps the navigation data at the sign-in application layer
// while the embedded Session issuance stays owned by the session service.
type Completed struct {
	appsession.Issued
	NextPath string
	IntentID string
}
