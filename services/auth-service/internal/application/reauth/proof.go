package reauth

import (
	"context"
	"errors"

	domainreauth "github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) ConsumeProofID(ctx context.Context, repository Repository, raw string, principal domainsession.Principal, purpose string) (uuid.UUID, error) {
	proof, err := repository.FindActiveForUpdate(ctx, s.cryptography.Hash("reauth", raw), principal.UserID, principal.SessionID, purpose)
	if errors.Is(err, domainreauth.ErrNotFound) {
		return uuid.Nil, invalidProof()
	}
	if err != nil {
		return uuid.Nil, unavailable(err)
	}
	if !proof.Active(s.clock.Now()) {
		return uuid.Nil, invalidProof()
	}
	if err := repository.Consume(ctx, proof.ID); err != nil {
		return uuid.Nil, unavailable(err)
	}
	return proof.ID, nil
}

var _ ProofConsumer = (*Service)(nil)
