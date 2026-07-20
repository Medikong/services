package cryptography

import applicationpasswordreset "github.com/Medikong/services/services/auth-service/internal/application/passwordreset"

type PasswordReset struct {
	keys Keys
}

func NewPasswordReset(keys Keys) *PasswordReset {
	return &PasswordReset{keys: keys}
}

func (c *PasswordReset) Hash(values ...string) []byte {
	return c.keys.Hash(values...)
}

func (c *PasswordReset) Equal(expected []byte, values ...string) bool {
	return c.keys.Equal(expected, values...)
}

func (c *PasswordReset) EqualHash(expected, actual []byte) bool {
	return c.keys.EqualHash(expected, actual)
}

func (c *PasswordReset) Opaque(prefix string) (string, error) {
	return c.keys.Opaque(prefix)
}

func (c *PasswordReset) VerificationCode() (string, error) {
	return c.keys.VerificationCode()
}

func (c *PasswordReset) Seal(value any) ([]byte, error) {
	return c.keys.Seal(value)
}

func (c *PasswordReset) SealVirtual(value any) ([]byte, error) {
	return c.keys.SealVirtual(value)
}

func (c *PasswordReset) HashPassword(password string) (string, error) {
	return HashPassword(password)
}

var _ applicationpasswordreset.Cryptography = (*PasswordReset)(nil)
