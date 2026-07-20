package userauthstate

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
)

type Config struct {
	StrongAuthTTL time.Duration
}

type Service struct {
	transactor  Transactor
	proofs      ProofVerifier
	decisions   AuthorizationDecisionPort
	strongTTL   time.Duration
	clock       Clock
	projection  StatusProjectionWriter
	revocations SessionRevocationFencer
}

type ApplyInput struct {
	Principal             domainsession.Principal
	PathUserID            string
	UserStatusChangeProof string
	AuthorizationDecision string
}

type ApplyOutput struct {
	UserID        uuid.UUID
	AccountStatus domainuserauthstate.Status
	UserVersion   int64
	Applied       bool
}

func NewService(transactor Transactor, proofs ProofVerifier, decisions AuthorizationDecisionPort, config Config, clock Clock, projections ...StatusProjectionWriter) *Service {
	if decisions == nil {
		decisions = DenyAuthorizationDecisionPort{}
	}
	if config.StrongAuthTTL <= 0 {
		config.StrongAuthTTL = 5 * time.Minute
	}
	if clock == nil {
		clock = wallClock{}
	}
	service := &Service{
		transactor: transactor,
		proofs:     proofs,
		decisions:  decisions,
		strongTTL:  config.StrongAuthTTL,
		clock:      clock,
	}
	if len(projections) > 0 {
		service.projection = projections[0]
	}
	return service
}

func (s *Service) UseSessionRevocation(fencer SessionRevocationFencer) {
	s.revocations = fencer
}

func (s *Service) Apply(ctx context.Context, input ApplyInput) (ApplyOutput, error) {
	if !input.Principal.Authenticated || input.Principal.UserID == uuid.Nil || input.Principal.SessionID == uuid.Nil {
		return ApplyOutput{}, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	now := s.clock.Now().UTC()
	authenticatedAt := input.Principal.AuthenticatedAt.UTC()
	if !strongMethod(input.Principal.Method) || authenticatedAt.IsZero() || authenticatedAt.After(now) || now.Sub(authenticatedAt) > s.strongTTL {
		return ApplyOutput{}, failure.Forbidden("AUTH_REAUTH_REQUIRED", "최근 강한 인증이 필요합니다.")
	}
	pathUserID, err := uuid.Parse(strings.TrimSpace(input.PathUserID))
	if err != nil || len(input.UserStatusChangeProof) < 32 || len(input.UserStatusChangeProof) > 8192 {
		return ApplyOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "사용자 상태 반영 요청이 올바르지 않습니다.")
	}
	if strings.TrimSpace(input.AuthorizationDecision) == "" {
		return ApplyOutput{}, failure.Forbidden("AUTH_FORBIDDEN", "이 작업을 수행할 수 없습니다.")
	}
	if err := s.decisions.Verify(ctx, input.AuthorizationDecision, "user.account_status.change", input.Principal.UserID.String(), pathUserID.String()); err != nil {
		return ApplyOutput{}, failure.Forbidden("AUTH_FORBIDDEN", "이 작업을 수행할 수 없습니다.")
	}
	proof, err := s.proofs.VerifyUserStatus(input.UserStatusChangeProof)
	if err != nil {
		return ApplyOutput{}, failure.Forbidden("AUTH_USER_STATUS_PROOF_INVALID", "사용자 상태 증명을 확인할 수 없습니다.")
	}
	proofUserID, userErr := uuid.Parse(proof.UserID)
	status, statusErr := domainuserauthstate.ParseStatus(proof.AccountStatus)
	if userErr != nil || proofUserID != pathUserID || statusErr != nil || proof.UserVersion < 1 || strings.TrimSpace(proof.StatusChangeID) == "" || proof.ChangedAt <= 0 {
		return ApplyOutput{}, failure.Forbidden("AUTH_USER_STATUS_PROOF_INVALID", "사용자 상태 증명을 확인할 수 없습니다.")
	}
	change := domainuserauthstate.Change{
		Status:         status,
		UserVersion:    proof.UserVersion,
		StatusChangeID: proof.StatusChangeID,
		ChangedAt:      time.Unix(proof.ChangedAt, 0).UTC(),
	}

	var current domainuserauthstate.State
	var apply, replay bool
	var fence domainsession.RevocationFence
	err = s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		var findErr error
		current, findErr = repositories.States.FindForUpdate(ctx, pathUserID)
		if errors.Is(findErr, domainuserauthstate.ErrNotFound) {
			return failure.NotFound("AUTH_OPERATOR_TARGET_NOT_FOUND", "운영 대상 인증 상태를 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		var compareErr error
		apply, replay, compareErr = current.Compare(change)
		if errors.Is(compareErr, domainuserauthstate.ErrVersionConflict) {
			return failure.Conflict("AUTH_RESOURCE_PRECONDITION_FAILED", "사용자 상태 version이 현재 상태와 충돌합니다.")
		}
		if compareErr != nil {
			return failure.Forbidden("AUTH_USER_STATUS_PROOF_INVALID", "사용자 상태 증명을 확인할 수 없습니다.")
		}
		if !apply {
			return nil
		}
		current, compareErr = repositories.States.Apply(ctx, current, change)
		if errors.Is(compareErr, domainuserauthstate.ErrVersionConflict) {
			return failure.Conflict("AUTH_RESOURCE_PRECONDITION_FAILED", "사용자 상태 version이 현재 상태와 충돌합니다.")
		}
		if compareErr != nil {
			return unavailable(compareErr)
		}
		if revokesSessions(status) {
			if repositories.Sessions == nil {
				return unavailable(nil)
			}
			if s.revocations != nil {
				targets, findErr := repositories.Sessions.FindActiveForUserForUpdate(ctx, pathUserID)
				if findErr != nil {
					return unavailable(findErr)
				}
				if len(targets) > 0 {
					var fenceErr error
					fence, fenceErr = s.revocations.Fence(ctx, targets)
					if fenceErr != nil {
						return unavailable(fenceErr)
					}
				}
			}
			if revokeErr := repositories.Sessions.RevokeForUser(ctx, pathUserID, "user_account_status_changed"); revokeErr != nil {
				return unavailable(revokeErr)
			}
		}
		return nil
	})
	if fence != nil {
		if resolveErr := fence.Resolve(context.WithoutCancel(ctx)); resolveErr != nil {
			return ApplyOutput{}, unavailable(resolveErr)
		}
	}
	if err != nil {
		return ApplyOutput{}, preserveFailure(err)
	}
	if fence == nil && s.projection != nil && revokesSessions(current.Status) {
		if err := s.projection.RevokeUser(ctx, pathUserID); err != nil {
			return ApplyOutput{}, unavailable(err)
		}
	}
	return ApplyOutput{
		UserID:        current.UserID,
		AccountStatus: current.Status,
		UserVersion:   current.UserVersion,
		Applied:       apply || replay,
	}, nil
}

func strongMethod(method string) bool {
	return method == "email_password" || method == "passkey"
}

func revokesSessions(status domainuserauthstate.Status) bool {
	return status == domainuserauthstate.StatusRestricted || status == domainuserauthstate.StatusDeactivated
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }
