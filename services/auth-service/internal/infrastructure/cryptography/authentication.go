package cryptography

import applicationauthentication "github.com/Medikong/services/services/auth-service/internal/application/authentication"

type Authentication struct {
	keys Keys
}

func NewAuthentication(keys Keys) *Authentication {
	return &Authentication{keys: keys}
}

func (c *Authentication) Hash(values ...string) []byte {
	return c.keys.Hash(values...)
}

func (c *Authentication) Equal(expected []byte, values ...string) bool {
	return c.keys.Equal(expected, values...)
}

func (c *Authentication) VerificationCode() (string, error) {
	return c.keys.VerificationCode()
}

func (*Authentication) VerifyPassword(hash, password string) bool {
	return VerifyPassword(hash, password)
}

func (c *Authentication) SealDelivery(code, destination string) ([]byte, error) {
	return c.keys.Seal(map[string]string{"code": code, "destination": destination})
}

func (c *Authentication) SealVirtualCode(code string) ([]byte, error) {
	return c.keys.SealVirtual(map[string]string{"code": code})
}

var _ applicationauthentication.Cryptography = (*Authentication)(nil)
