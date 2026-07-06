package session

const (
	AuthMethodPassword  = "password"
	AuthMethodTestToken = "test_token"
)

type Input struct {
	AuthAccountID string
	UserID        string
	AuthMethods   []string
}

type Record struct {
	SessionID     string
	AccessToken   string
	RefreshToken  string
	AuthAccountID string
	UserID        string
	AuthMethods   []string
}

type Rotation struct {
	PreviousAccessToken string
	Session             Record
}
