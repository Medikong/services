package passwordauth

import (
	"time"
)

type PasswordCredential struct {
	CredentialID  int64
	AuthAccountID string
	Email         string
	PasswordHash  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
