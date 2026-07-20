package cryptography

import (
	"fmt"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/google/uuid"
)

type Session struct {
	keys Keys
}

func NewSession(keys Keys) *Session {
	return &Session{keys: keys}
}

func (c *Session) Hash(values ...string) []byte {
	return c.keys.Hash(values...)
}

func (c *Session) Equal(expected []byte, values ...string) bool {
	return c.keys.Equal(expected, values...)
}

func (c *Session) Opaque(prefix string) (string, error) {
	return c.keys.Opaque(prefix)
}

func (c *Session) SealTokenSet(tokens applicationsession.TokenSet) ([]byte, error) {
	return c.keys.Seal(tokens)
}

func (c *Session) OpenTokenSet(ciphertext []byte) (applicationsession.TokenSet, error) {
	var tokens applicationsession.TokenSet
	if err := c.keys.Open(ciphertext, &tokens); err != nil {
		return applicationsession.TokenSet{}, err
	}
	return tokens, nil
}

func (c *Session) SignAccessToken(userID, sessionID uuid.UUID, ttl time.Duration) (string, time.Time, error) {
	return c.keys.SignAccessToken(userID.String(), sessionID.String(), ttl)
}

func (c *Session) VerifyAccessToken(raw string) (applicationsession.AccessClaims, error) {
	claims, err := c.keys.VerifyAccessToken(raw)
	if err != nil {
		return applicationsession.AccessClaims{}, err
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil || userID == uuid.Nil {
		return applicationsession.AccessClaims{}, fmt.Errorf("invalid access token subject")
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil || sessionID == uuid.Nil {
		return applicationsession.AccessClaims{}, fmt.Errorf("invalid access token session ID")
	}
	return applicationsession.AccessClaims{UserID: userID, SessionID: sessionID, TokenID: claims.TokenID}, nil
}

var _ applicationsession.Cryptography = (*Session)(nil)
