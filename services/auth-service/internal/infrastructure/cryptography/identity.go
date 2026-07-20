package cryptography

import applicationidentity "github.com/Medikong/services/services/auth-service/internal/application/identity"

type Identity struct {
	keys Keys
}

func NewIdentity(keys Keys) *Identity {
	return &Identity{keys: keys}
}

func (c *Identity) Hash(values ...string) []byte {
	return c.keys.Hash(values...)
}

func (c *Identity) Equal(expected []byte, values ...string) bool {
	return c.keys.Equal(expected, values...)
}

func (c *Identity) VerificationCode() (string, error) {
	return c.keys.VerificationCode()
}

func (c *Identity) SealDelivery(code, destination string) ([]byte, error) {
	return c.keys.Seal(map[string]string{"code": code, "destination": destination})
}

func (c *Identity) SealVirtualCode(code string) ([]byte, error) {
	return c.keys.SealVirtual(map[string]string{"code": code})
}

func (c *Identity) SealStartOutput(output applicationidentity.StartLinkOutput) ([]byte, error) {
	return c.keys.Seal(output)
}

func (c *Identity) OpenStartOutput(ciphertext []byte) (applicationidentity.StartLinkOutput, error) {
	var output applicationidentity.StartLinkOutput
	if err := c.keys.Open(ciphertext, &output); err != nil {
		return applicationidentity.StartLinkOutput{}, err
	}
	return output, nil
}

func (c *Identity) SealCompleteOutput(output applicationidentity.CompleteLinkOutput) ([]byte, error) {
	return c.keys.Seal(output)
}

func (c *Identity) OpenCompleteOutput(ciphertext []byte) (applicationidentity.CompleteLinkOutput, error) {
	var output applicationidentity.CompleteLinkOutput
	if err := c.keys.Open(ciphertext, &output); err != nil {
		return applicationidentity.CompleteLinkOutput{}, err
	}
	return output, nil
}

var _ applicationidentity.Cryptography = (*Identity)(nil)
