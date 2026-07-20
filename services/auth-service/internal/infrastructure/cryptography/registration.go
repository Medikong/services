package cryptography

type RegistrationPasswordHasher struct{}

func (RegistrationPasswordHasher) HashPassword(password string) (string, error) {
	return HashPassword(password)
}

type RegistrationProofVerifier struct {
	verifier UserProofVerifier
}

func NewRegistrationProofVerifier(verifier UserProofVerifier) RegistrationProofVerifier {
	return RegistrationProofVerifier{verifier: verifier}
}

func (v RegistrationProofVerifier) VerifyUserCreation(raw string) (string, string, int64, error) {
	claims, err := v.verifier.VerifyUserCreation(raw)
	if err != nil {
		return "", "", 0, err
	}
	return claims.RegistrationID, claims.UserID, claims.UserVersion, nil
}
