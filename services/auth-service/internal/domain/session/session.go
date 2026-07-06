package session

import "time"

const (
	AuthMethodPassword  = "password"
	AuthMethodTestToken = "test_token"
)

type Input struct {
	AuthAccountID    string
	UserID           string
	Email            string
	AccessJTI        string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	AuthMethods      []string
}

type Record struct {
	SessionID        string
	AccessToken      string
	AccessJTI        string
	RefreshToken     string
	AuthAccountID    string
	UserID           string
	Email            string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	AuthMethods      []string
}

type Rotation struct {
	PreviousAccessToken string
	Session             Record
}
