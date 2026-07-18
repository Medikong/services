package userauthstate

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/security"
)

type ProofVerifier interface {
	VerifyUserStatus(string) (security.UserStatusProof, error)
}

type AuthorizationDecisionPort interface {
	Verify(context.Context, string, string, string, string) error
}

type DenyAuthorizationDecisionPort struct{}

func (DenyAuthorizationDecisionPort) Verify(context.Context, string, string, string, string) error {
	return errors.New("authorization decision verifier is not configured")
}

type SessionRevoker interface {
	FenceRevocationsForUser(context.Context, pgx.Tx, uuid.UUID) (domain.RevocationFences, error)
	RevokeForUser(context.Context, pgx.Tx, uuid.UUID, string) error
	ProjectRevokedForUser(context.Context, uuid.UUID) error
}

type Config struct {
	StrongAuthTTL time.Duration
}

type Service struct {
	pool      *pgxpool.Pool
	states    Repository
	sessions  SessionRevoker
	proofs    ProofVerifier
	decisions AuthorizationDecisionPort
	strongTTL time.Duration
	now       func() time.Time
}

type ApplyInput struct {
	Principal             domain.Principal
	PathUserID            string
	UserStatusChangeProof string
	AuthorizationDecision string
}

type ApplyOutput struct {
	UserID        uuid.UUID
	AccountStatus Status
	UserVersion   int64
	Applied       bool
}

func NewService(pool *pgxpool.Pool, states Repository, sessions SessionRevoker, proofs ProofVerifier, decisions AuthorizationDecisionPort, config Config) *Service {
	if decisions == nil {
		decisions = DenyAuthorizationDecisionPort{}
	}
	if config.StrongAuthTTL <= 0 {
		config.StrongAuthTTL = 5 * time.Minute
	}
	return &Service{pool: pool, states: states, sessions: sessions, proofs: proofs, decisions: decisions, strongTTL: config.StrongAuthTTL, now: time.Now}
}

func (s *Service) Apply(ctx context.Context, input ApplyInput) (ApplyOutput, error) {
	if !input.Principal.Authenticated || input.Principal.UserID == uuid.Nil || input.Principal.SessionID == uuid.Nil {
		return ApplyOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if !strongMethod(input.Principal.Method) || input.Principal.AuthenticatedAt.IsZero() || s.now().UTC().Sub(input.Principal.AuthenticatedAt.UTC()) > s.strongTTL {
		return ApplyOutput{}, domain.Problem(403, "AUTH_REAUTH_REQUIRED", "최근 강한 인증이 필요합니다.")
	}
	pathUserID, err := uuid.Parse(strings.TrimSpace(input.PathUserID))
	if err != nil || len(input.UserStatusChangeProof) < 32 || len(input.UserStatusChangeProof) > 8192 {
		return ApplyOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "사용자 상태 반영 요청이 올바르지 않습니다.")
	}
	if strings.TrimSpace(input.AuthorizationDecision) == "" {
		return ApplyOutput{}, domain.Problem(403, "AUTH_FORBIDDEN", "이 작업을 수행할 수 없습니다.")
	}
	if err := s.decisions.Verify(ctx, input.AuthorizationDecision, "user.account_status.change", input.Principal.UserID.String(), pathUserID.String()); err != nil {
		return ApplyOutput{}, domain.Problem(403, "AUTH_FORBIDDEN", "이 작업을 수행할 수 없습니다.")
	}
	proof, err := s.proofs.VerifyUserStatus(input.UserStatusChangeProof)
	if err != nil {
		return ApplyOutput{}, domain.Problem(403, "AUTH_USER_STATUS_PROOF_INVALID", "사용자 상태 증명을 확인할 수 없습니다.")
	}
	proofUserID, userErr := uuid.Parse(proof.UserID)
	status, statusErr := ParseStatus(proof.AccountStatus)
	if userErr != nil || proofUserID != pathUserID || statusErr != nil || proof.UserVersion < 1 || strings.TrimSpace(proof.StatusChangeID) == "" || proof.ChangedAt <= 0 {
		return ApplyOutput{}, domain.Problem(403, "AUTH_USER_STATUS_PROOF_INVALID", "사용자 상태 증명을 확인할 수 없습니다.")
	}
	change := Change{Status: status, UserVersion: proof.UserVersion, StatusChangeID: proof.StatusChangeID, ChangedAt: time.Unix(proof.ChangedAt, 0).UTC()}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, err := s.states.FindForUpdate(ctx, tx, pathUserID)
	if errors.Is(err, ErrNotFound) {
		return ApplyOutput{}, domain.Problem(404, "AUTH_OPERATOR_TARGET_NOT_FOUND", "운영 대상 인증 상태를 찾을 수 없습니다.")
	}
	if err != nil {
		return ApplyOutput{}, domain.Unavailable()
	}
	apply, replay, err := current.Compare(change)
	if errors.Is(err, ErrVersionConflict) {
		return ApplyOutput{}, domain.Problem(412, "AUTH_RESOURCE_PRECONDITION_FAILED", "사용자 상태 version이 현재 상태와 충돌합니다.")
	}
	if err != nil {
		return ApplyOutput{}, domain.Problem(403, "AUTH_USER_STATUS_PROOF_INVALID", "사용자 상태 증명을 확인할 수 없습니다.")
	}
	var fences domain.RevocationFences
	if apply {
		current, err = s.states.Apply(ctx, tx, current, change)
		if errors.Is(err, ErrVersionConflict) {
			return ApplyOutput{}, domain.Problem(412, "AUTH_RESOURCE_PRECONDITION_FAILED", "사용자 상태 version이 현재 상태와 충돌합니다.")
		}
		if err != nil {
			return ApplyOutput{}, domain.Unavailable()
		}
		if status == StatusRestricted || status == StatusDeactivated {
			fences, err = s.sessions.FenceRevocationsForUser(ctx, tx, pathUserID)
			if err != nil {
				domain.ResolveRevocationRollback(ctx, tx, fences)
				return ApplyOutput{}, domain.Unavailable()
			}
			if err := s.sessions.RevokeForUser(ctx, tx, pathUserID, "user_account_status_changed"); err != nil {
				domain.ResolveRevocationRollback(ctx, tx, fences)
				return ApplyOutput{}, domain.Unavailable()
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		domain.ResolveRevocationRollback(ctx, tx, fences)
		return ApplyOutput{}, domain.Unavailable()
	}
	if fences != nil {
		if err := fences.Resolve(ctx); err != nil {
			return ApplyOutput{}, domain.Unavailable()
		}
	} else if current.Status == StatusRestricted || current.Status == StatusDeactivated {
		if err := s.sessions.ProjectRevokedForUser(ctx, pathUserID); err != nil {
			return ApplyOutput{}, domain.Unavailable()
		}
	}
	return ApplyOutput{UserID: current.UserID, AccountStatus: current.Status, UserVersion: current.UserVersion, Applied: apply || replay}, nil
}

func strongMethod(method string) bool {
	switch method {
	case "email_password", "passkey":
		return true
	default:
		return false
	}
}
