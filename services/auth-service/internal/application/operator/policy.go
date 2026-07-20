package operator

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainoperator "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	domainpolicy "github.com/Medikong/services/services/auth-service/internal/domain/policy"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) PolicyView(ctx context.Context, principal domainsession.Principal, decision string) (PolicyView, error) {
	if err := s.authorize(ctx, principal, decision, false, "auth.policy.read", "auth-policies"); err != nil {
		return PolicyView{}, err
	}
	var view PolicyView
	err := s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		snapshot, findErr := repositories.Policies.FindGlobalActive(ctx)
		if findErr != nil {
			return unavailable(findErr)
		}
		var viewErr error
		view, viewErr = policyViewFromGlobal(snapshot)
		if viewErr != nil {
			return unavailable(viewErr)
		}
		return nil
	})
	if err != nil {
		return PolicyView{}, preserveFailure(err)
	}
	return view, nil
}

type PolicyUpdateInput struct {
	Principal                                            domainsession.Principal
	Name, IfMatch, IdempotencyKey, AuthorizationDecision string
	Patch                                                map[string]any
}

type PolicyUpdateOutput struct {
	Name        string
	Version     int64
	Status      string
	EffectiveAt time.Time
}

func (s *Service) UpdatePolicy(ctx context.Context, input PolicyUpdateInput) (PolicyUpdateOutput, error) {
	if err := s.authorize(ctx, input.Principal, input.AuthorizationDecision, true, "auth.policy.write", input.Name); err != nil {
		return PolicyUpdateOutput{}, err
	}
	name := domainoperator.NormalizePolicyName(input.Name)
	if name == "" {
		return PolicyUpdateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "정책 변경 요청이 올바르지 않습니다.")
	}
	if _, err := uuid.Parse(input.IdempotencyKey); err != nil {
		return PolicyUpdateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "Idempotency-Key는 UUID여야 합니다.")
	}
	reason, _ := input.Patch["changeReason"].(string)
	bodyName, _ := input.Patch["policyName"].(string)
	if domainoperator.NormalizePolicyName(bodyName) != name || strings.TrimSpace(reason) == "" {
		return PolicyUpdateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "정책 이름과 변경 사유를 확인해주세요.")
	}
	requestBody, err := json.Marshal(input.Patch)
	if err != nil {
		return PolicyUpdateOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "정책 변경 요청이 올바르지 않습니다.")
	}
	scopeHash := s.crypto.Hash("operator_policy_update", input.Principal.UserID.String())
	keyHash := s.crypto.Hash(input.IdempotencyKey)
	requestHash := s.crypto.Hash(name, string(requestBody))
	candidate := domainidempotency.NewRecord("operator_policy_update", scopeHash, keyHash, requestHash, nil, nil, s.clock.Now().UTC().Add(time.Hour))
	var output PolicyUpdateOutput
	err = s.transactor.WithinTransaction(ctx, func(repositories TxRepositories) error {
		record, claimed, recordErr := repositories.Idempotency.ClaimProcessing(ctx, candidate, "PolicyGlobalSnapshot")
		if recordErr != nil {
			return unavailable(recordErr)
		}
		if !claimed {
			if !s.crypto.EqualHash(record.RequestHash, requestHash) {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
			}
			if record.Status != "completed" || record.ReplayID == nil {
				return failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", unavailableMessage)
			}
			var replayErr error
			output, replayErr = s.replayPolicyUpdate(ctx, repositories.Idempotency, *record.ReplayID, input)
			return replayErr
		}
		global, findErr := repositories.Policies.FindGlobalActiveForUpdate(ctx)
		if errors.Is(findErr, domainpolicy.ErrNotFound) {
			return failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", unavailableMessage)
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		active, listErr := repositories.Policies.ListActiveForUpdate(ctx)
		if listErr != nil {
			return unavailable(listErr)
		}
		expected, ok := parseETag(input.IfMatch)
		if !ok || expected != global.Version {
			return failure.Conflict("AUTH_POLICY_PRECONDITION_FAILED", "정책 version이 현재 상태와 다릅니다.")
		}
		var previous domainpolicy.Snapshot
		for _, snapshot := range active {
			if snapshot.Name == name {
				previous = snapshot
				break
			}
		}
		if previous.Name == "" {
			return failure.Invalid("AUTH_INPUT_INVALID", "알 수 없는 정책입니다.")
		}
		raw, patchErr := domainoperator.PatchPolicyRules(name, previous.Rules, input.Patch)
		if patchErr != nil {
			return failure.Invalid("AUTH_INPUT_INVALID", "정책 값이 계약을 만족하지 않습니다.")
		}
		updated, updateErr := repositories.Policies.SupersedeAndInsert(ctx, previous, raw, reason, input.Principal.UserID)
		if updateErr != nil {
			return unavailable(updateErr)
		}
		for index := range active {
			if active[index].Name == name {
				active[index] = updated
				break
			}
		}
		view, viewErr := policyView(active)
		if viewErr != nil {
			return unavailable(viewErr)
		}
		document, documentErr := view.document()
		if documentErr != nil {
			return unavailable(documentErr)
		}
		global, updateErr = repositories.Policies.ActivateGlobal(ctx, document, input.Principal.UserID, reason)
		if updateErr != nil {
			return unavailable(updateErr)
		}
		output = PolicyUpdateOutput{Name: publicPolicyName(updated.Name), Version: global.Version, Status: global.Status, EffectiveAt: global.EffectiveAt}
		ciphertext, sealErr := s.crypto.SealPolicyUpdate(output)
		if sealErr != nil {
			return unavailable(sealErr)
		}
		replayID := uuid.New()
		if createErr := repositories.Idempotency.CreateReplayPayload(ctx, domainidempotency.ReplayPayload{ID: replayID, Kind: "operator_policy_update", Ciphertext: ciphertext, BindingHash: s.policyReplayBinding(input), ExpiresAt: record.ExpiresAt}); createErr != nil {
			return unavailable(createErr)
		}
		if attachErr := repositories.Idempotency.AttachReplayPayload(ctx, record.ID, replayID); attachErr != nil {
			return unavailable(attachErr)
		}
		if completeErr := repositories.Idempotency.Complete(ctx, record.ID, "completed"); completeErr != nil {
			return unavailable(completeErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.policy.updated", "operator", input.Principal.UserID, uuid.New(), map[string]string{"policy": name}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		return nil
	})
	if err != nil {
		return PolicyUpdateOutput{}, preserveFailure(err)
	}
	return output, nil
}

func (s *Service) replayPolicyUpdate(ctx context.Context, idempotency IdempotencyRepository, replayID uuid.UUID, input PolicyUpdateInput) (PolicyUpdateOutput, error) {
	payload, err := idempotency.FindReplayPayloadForUpdate(ctx, replayID)
	if err != nil || payload.Kind != "operator_policy_update" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(s.clock.Now().UTC()) || !s.crypto.EqualHash(payload.BindingHash, s.policyReplayBinding(input)) {
		return PolicyUpdateOutput{}, failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", unavailableMessage)
	}
	output, err := s.crypto.OpenPolicyUpdate(payload.Ciphertext)
	if err != nil {
		return PolicyUpdateOutput{}, unavailable(err)
	}
	if err := idempotency.RecordReplay(ctx, replayID); err != nil {
		return PolicyUpdateOutput{}, unavailable(err)
	}
	return output, nil
}

func (s *Service) policyReplayBinding(input PolicyUpdateInput) []byte {
	return s.crypto.Hash("operator_policy_update", input.Principal.UserID.String(), input.IdempotencyKey)
}

func parseETag(value string) (int64, bool) {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if !strings.HasPrefix(value, "policy-") {
		return 0, false
	}
	result, err := strconv.ParseInt(strings.TrimPrefix(value, "policy-"), 10, 64)
	return result, err == nil
}
