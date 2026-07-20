package cryptography

import applicationreauth "github.com/Medikong/services/services/auth-service/internal/application/reauth"

type Reauthentication struct {
	keys Keys
}

func NewReauthentication(keys Keys) *Reauthentication {
	return &Reauthentication{keys: keys}
}

func (c *Reauthentication) Hash(values ...string) []byte {
	return c.keys.Hash(values...)
}

func (c *Reauthentication) Equal(expected []byte, values ...string) bool {
	return c.keys.Equal(expected, values...)
}

func (c *Reauthentication) Opaque(prefix string) (string, error) {
	return c.keys.Opaque(prefix)
}

func (*Reauthentication) VerifyPassword(hash, password string) bool {
	return VerifyPassword(hash, password)
}

func (c *Reauthentication) SealOutput(output applicationreauth.Output) ([]byte, error) {
	return c.keys.Seal(output)
}

func (c *Reauthentication) OpenOutput(ciphertext []byte) (applicationreauth.Output, error) {
	var output applicationreauth.Output
	if err := c.keys.Open(ciphertext, &output); err != nil {
		return applicationreauth.Output{}, err
	}
	return output, nil
}

var _ applicationreauth.Cryptography = (*Reauthentication)(nil)
