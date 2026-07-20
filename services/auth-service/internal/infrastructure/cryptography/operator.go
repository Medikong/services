package cryptography

import applicationoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"

type OperatorCryptography struct {
	keys Keys
}

func NewOperatorCryptography(keys Keys) *OperatorCryptography {
	return &OperatorCryptography{keys: keys}
}

func (c *OperatorCryptography) Hash(values ...string) []byte {
	return c.keys.Hash(values...)
}

func (c *OperatorCryptography) SealPolicyUpdate(output applicationoperator.PolicyUpdateOutput) ([]byte, error) {
	return c.keys.Seal(output)
}

func (c *OperatorCryptography) OpenPolicyUpdate(ciphertext []byte) (applicationoperator.PolicyUpdateOutput, error) {
	var output applicationoperator.PolicyUpdateOutput
	if err := c.keys.Open(ciphertext, &output); err != nil {
		return applicationoperator.PolicyUpdateOutput{}, err
	}
	return output, nil
}

var _ applicationoperator.Cryptography = (*OperatorCryptography)(nil)
