package operator

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainoperator "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type ManualInput struct {
	Principal                                                                                                        domainsession.Principal
	CaseID, TargetType, TargetID, Action, ReasonCode, ApprovalID, EvidenceRef, IdempotencyKey, AuthorizationDecision string
	ExpectedVersion                                                                                                  int64
}

func (s *Service) Manual(ctx context.Context, input ManualInput) (uuid.UUID, int64, error) {
	if !validManual(input) {
		return uuid.Nil, 0, failure.Invalid("AUTH_INPUT_INVALID", "운영 작업 요청이 올바르지 않습니다.")
	}
	if err := s.authorize(ctx, input.Principal, input.AuthorizationDecision, true, manualPermission(input.Action), input.TargetType+":"+input.TargetID); err != nil {
		return uuid.Nil, 0, err
	}
	if s.approvals.Verify(ctx, ApprovalRequest{CaseID: input.CaseID, ApprovalID: input.ApprovalID, EvidenceRef: input.EvidenceRef, Action: input.Action, TargetType: input.TargetType, TargetID: input.TargetID}) != nil {
		return uuid.Nil, 0, failure.Conflict("AUTH_APPROVAL_REQUIRED", "승인된 운영 작업이 필요합니다.")
	}
	if _, err := uuid.Parse(input.IdempotencyKey); err != nil {
		return uuid.Nil, 0, failure.Invalid("AUTH_INPUT_INVALID", "Idempotency-Key는 UUID여야 합니다.")
	}
	scope := s.crypto.Hash("manual_auth_action", input.Principal.UserID.String())
	keyHash := s.crypto.Hash(input.IdempotencyKey)
	requestHash := s.crypto.Hash(input.Action, input.TargetType, input.TargetID, input.ReasonCode)
	var actionID uuid.UUID
	var version int64
	var fence domainsession.RevocationFence
	err := s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		record, findErr := repositories.Idempotency.FindForUpdate(ctx, "manual_auth_action", scope, keyHash)
		if findErr == nil {
			if !s.crypto.EqualHash(record.RequestHash, requestHash) {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
			}
			if record.Status != "completed" || record.ResourceID == nil {
				return failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", unavailableMessage)
			}
			result, resultErr := repositories.Operators.FindManualResult(ctx, *record.ResourceID)
			if resultErr != nil {
				return unavailable(resultErr)
			}
			actionID, version = result.ActionID, result.TargetVersion
			return nil
		}
		if !errors.Is(findErr, domainidempotency.ErrNotFound) {
			return unavailable(findErr)
		}
		actionID = uuid.New()
		record = domainidempotency.NewRecord("manual_auth_action", scope, keyHash, requestHash, &actionID, nil, s.clock.Now().UTC().Add(time.Hour))
		if createErr := repositories.Idempotency.CreateProcessing(ctx, record, "ManualAction"); createErr != nil {
			return unavailable(createErr)
		}
		if input.Action == "revoke_sessions" {
			targetID, parseErr := uuid.Parse(input.TargetID)
			if parseErr != nil {
				return failure.Invalid("AUTH_INPUT_INVALID", "운영 작업 요청이 올바르지 않습니다.")
			}
			if s.revocations != nil {
				if repositories.Sessions == nil {
					return unavailable(nil)
				}
				target, findErr := repositories.Sessions.FindActiveForUpdate(ctx, targetID)
				if findErr != nil {
					return unavailable(findErr)
				}
				var fenceErr error
				fence, fenceErr = s.revocations.Fence(ctx, []domainsession.Session{target})
				if fenceErr != nil {
					return unavailable(fenceErr)
				}
			}
		}
		action := domainoperator.ManualAction{ID: actionID, OperatorID: input.Principal.UserID, CaseID: input.CaseID, TargetType: input.TargetType, TargetID: input.TargetID, Action: input.Action, ReasonCode: input.ReasonCode, ApprovalID: input.ApprovalID, EvidenceRef: input.EvidenceRef, ExpectedVersion: input.ExpectedVersion, IdempotencyID: &record.ID}
		var applyErr error
		version, applyErr = repositories.Operators.ApplyManual(ctx, action)
		if errors.Is(applyErr, domainoperator.ErrNotFound) {
			return failure.Conflict("AUTH_RESOURCE_PRECONDITION_FAILED", "대상 version이 현재 상태와 다릅니다.")
		}
		if applyErr != nil {
			return unavailable(applyErr)
		}
		if completeErr := repositories.Idempotency.Complete(ctx, record.ID, "completed"); completeErr != nil {
			return unavailable(completeErr)
		}
		if outboxErr := repositories.Outbox.Append(ctx, domainoutbox.Event{ID: uuid.New(), Type: "Auth.ManualActionCompleted", AggregateType: "ManualAction", AggregateID: actionID, Version: version, Payload: json.RawMessage(`{"status":"completed"}`), CorrelationID: input.Principal.SessionID}); outboxErr != nil {
			return unavailable(outboxErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.manual_action.completed", "operator", input.Principal.UserID, actionID, map[string]string{"action": input.Action}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		return nil
	})
	if fence != nil {
		if resolveErr := fence.Resolve(context.WithoutCancel(ctx)); resolveErr != nil {
			return uuid.Nil, 0, unavailable(resolveErr)
		}
	}
	if err != nil {
		return uuid.Nil, 0, preserveFailure(err)
	}
	return actionID, version, nil
}

func manualPermission(action string) string {
	switch action {
	case "unlock_identity":
		return "auth.identity.unlock"
	case "revoke_identity_link":
		return "auth.identity_link.revoke"
	case "approve_relink":
		return "auth.identity_link.relink"
	case "revoke_sessions":
		return "auth.session.revoke"
	default:
		return ""
	}
}

func validManual(value ManualInput) bool {
	if value.CaseID == "" || value.TargetID == "" || value.ReasonCode == "" || value.ApprovalID == "" || value.EvidenceRef == "" || value.ExpectedVersion < 0 {
		return false
	}
	return domainoperator.ValidateManualTarget(value.Action, value.TargetType) == nil
}
