package cryptography

import applicationuserauthstate "github.com/Medikong/services/services/auth-service/internal/application/userauthstate"

type UserAuthStateProofVerifier struct {
	verifier UserProofVerifier
}

func NewUserAuthStateProofVerifier(verifier UserProofVerifier) *UserAuthStateProofVerifier {
	return &UserAuthStateProofVerifier{verifier: verifier}
}

func (v *UserAuthStateProofVerifier) VerifyUserStatus(raw string) (applicationuserauthstate.StatusProof, error) {
	proof, err := v.verifier.VerifyUserStatus(raw)
	if err != nil {
		return applicationuserauthstate.StatusProof{}, err
	}
	return applicationuserauthstate.StatusProof{
		StatusChangeID: proof.StatusChangeID,
		UserID:         proof.UserID,
		AccountStatus:  proof.AccountStatus,
		UserVersion:    proof.UserVersion,
		ChangedAt:      proof.ChangedAt,
	}, nil
}

var _ applicationuserauthstate.ProofVerifier = (*UserAuthStateProofVerifier)(nil)
