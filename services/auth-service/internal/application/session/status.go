package session

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const statusRequiredMessage = "인증 정보를 확인한 뒤 다시 시도해주세요."

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
	if s == nil || s.verifier == nil || s.status == nil {
		return StatusIdentity{}, unavailable(errors.New("session status service is not configured"))
	}
	claims, err := s.verifier.VerifyAccessToken(raw)
	if err != nil {
		return StatusIdentity{}, unauthenticated("AUTH_SESSION_REQUIRED", statusRequiredMessage)
	}
	tokenID, err := uuid.Parse(claims.TokenID)
	if err != nil || claims.UserID == uuid.Nil || claims.SessionID == uuid.Nil || tokenID == uuid.Nil {
		return StatusIdentity{}, unauthenticated("AUTH_SESSION_REQUIRED", statusRequiredMessage)
	}
	allowed, err := s.status.Check(ctx, claims.UserID, claims.SessionID)
	if err != nil {
		return StatusIdentity{}, unavailable(err)
	}
	if err := ctx.Err(); err != nil {
		return StatusIdentity{}, unavailable(err)
	}
	if !allowed {
		return StatusIdentity{}, unauthenticated("AUTH_SESSION_REVOKED", "Session을 사용할 수 없습니다.")
	}
	return StatusIdentity{UserID: claims.UserID, SessionID: claims.SessionID, TokenID: tokenID.String()}, nil
}
