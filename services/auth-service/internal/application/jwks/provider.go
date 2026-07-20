package jwks

type Key struct {
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

type Set struct {
	Keys []Key `json:"keys"`
}

// Provider exposes only the public verification keys required by HTTP.
type Provider interface {
	JWKS() (Set, error)
}
