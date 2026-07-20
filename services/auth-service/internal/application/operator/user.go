package operator

import (
	"context"
	"errors"
	"regexp"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainoperator "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) User(ctx context.Context, principal domainsession.Principal, decision, userID, reasonCode, auditKey string) (domainoperator.UserView, error) {
	if err := s.authorize(ctx, principal, decision, false, "auth.case.read", userID); err != nil {
		return domainoperator.UserView{}, err
	}
	if !validAuditReason(reasonCode) {
		return domainoperator.UserView{}, failure.Invalid("AUTH_INPUT_INVALID", "감사 사유 코드가 올바르지 않습니다.")
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return domainoperator.UserView{}, failure.Invalid("AUTH_INPUT_INVALID", "사용자 식별자가 올바르지 않습니다.")
	}
	var view domainoperator.UserView
	err = s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		var findErr error
		view, findErr = repositories.Operators.GetUser(ctx, id)
		if errors.Is(findErr, domainoperator.ErrNotFound) {
			return failure.NotFound("AUTH_OPERATOR_TARGET_NOT_FOUND", "운영 대상 인증 상태를 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.operator_user.viewed", "operator", principal.UserID, id, map[string]string{"reasonCode": reasonCode}, auditKey); auditErr != nil {
			return unavailable(auditErr)
		}
		return nil
	})
	if err != nil {
		return domainoperator.UserView{}, preserveFailure(err)
	}
	return view, nil
}

var auditReasonCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

func validAuditReason(value string) bool {
	return len(value) <= 64 && auditReasonCode.MatchString(value)
}
