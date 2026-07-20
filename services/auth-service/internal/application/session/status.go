package session

import (
	"context"

	"github.com/google/uuid"
)

type AccessTokenVerifier interface {
	VerifyAccessToken(string) (AccessClaims, error)
}

type StatusReader interface {
	Check(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

type StatusIdentity struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	TokenID   string
}

type StatusService struct {
	verifier AccessTokenVerifier
	status   StatusReader
}

func NewStatusService(verifier AccessTokenVerifier, status StatusReader) *StatusService {
	return &StatusService{verifier: verifier, status: status}
}

func (s *StatusService) Validate(ctx context.Context, raw string) (StatusIdentity, error) {
	claims, err := s.verifier.VerifyAccessToken(raw)
	if err != nil {
		return StatusIdentity{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	allowed, err := s.status.Check(ctx, claims.UserID, claims.SessionID)
	if err != nil {
		return StatusIdentity{}, unavailable(err)
	}
	if !allowed {
		return StatusIdentity{}, unauthenticated("AUTH_SESSION_REVOKED", "Session을 사용할 수 없습니다.")
	}
	return StatusIdentity{UserID: claims.UserID, SessionID: claims.SessionID, TokenID: claims.TokenID}, nil
}
