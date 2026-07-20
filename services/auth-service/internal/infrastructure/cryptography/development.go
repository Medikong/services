package cryptography

import applicationdevelopment "github.com/Medikong/services/services/auth-service/internal/application/development"

type Development struct {
	keys Keys
}

func NewDevelopment(keys Keys) *Development {
	return &Development{keys: keys}
}

func (c *Development) OpenVirtual(ciphertext []byte, target any) error {
	return c.keys.OpenVirtual(ciphertext, target)
}

var _ applicationdevelopment.Cryptography = (*Development)(nil)
