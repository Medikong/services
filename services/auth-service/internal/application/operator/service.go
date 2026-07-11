package operator

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	operatordomain "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	users       operatordomain.Repository
	policies    policy.Repository
	access      access.Repository
	idempotency idempotency.Repository
	outbox      outbox.Repository
	approvals   ApprovalPort
	strongTTL   time.Duration
}

type Config struct {
	StrongAuthTTL time.Duration
}

func NewService(pool *pgxpool.Pool, keys security.Keys, users operatordomain.Repository, policies policy.Repository, access access.Repository, idempotency idempotency.Repository, outbox outbox.Repository, config Config, approvals ApprovalPort) *Service {
	if approvals == nil {
		approvals = DenyApprovalPort{}
	}
	if config.StrongAuthTTL <= 0 {
		config.StrongAuthTTL = 5 * time.Minute
	}
	return &Service{pool: pool, keys: keys, users: users, policies: policies, access: access, idempotency: idempotency, outbox: outbox, approvals: approvals, strongTTL: config.StrongAuthTTL}
}
func (s *Service) User(ctx context.Context, principal appsession.Principal, userID, reasonCode, auditKey string) (operatordomain.UserView, error) {
	if err := s.authorize(ctx, principal, false, "auth.case.read"); err != nil {
		return operatordomain.UserView{}, err
	}
	if !validAuditReason(reasonCode) {
		return operatordomain.UserView{}, application.Problem(400, "AUTH_INPUT_INVALID", "감사 사유 코드가 올바르지 않습니다.")
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return operatordomain.UserView{}, application.Problem(400, "AUTH_INPUT_INVALID", "사용자 식별자가 올바르지 않습니다.")
	}
	view, err := s.users.GetUser(ctx, id)
	if errors.Is(err, operatordomain.ErrNotFound) {
		return operatordomain.UserView{}, application.Problem(404, "AUTH_OPERATOR_TARGET_NOT_FOUND", "운영 대상 인증 상태를 찾을 수 없습니다.")
	}
	if err != nil {
		return operatordomain.UserView{}, application.Unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return operatordomain.UserView{}, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := application.AppendAudit(ctx, tx, "auth.operator_user.viewed", "operator", principal.UserID, id, map[string]string{"reasonCode": reasonCode}, auditKey); err != nil {
		return operatordomain.UserView{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return operatordomain.UserView{}, application.Unavailable()
	}
	return view, nil
}
func (s *Service) PolicyView(ctx context.Context, principal appsession.Principal) (PolicyView, error) {
	if err := s.authorize(ctx, principal, false, "auth.policy.read"); err != nil {
		return PolicyView{}, err
	}
	snapshot, err := s.policies.FindGlobalActive(ctx)
	if err != nil {
		return PolicyView{}, application.Unavailable()
	}
	view, err := policyViewFromGlobal(snapshot)
	if err != nil {
		return PolicyView{}, application.Unavailable()
	}
	return view, nil
}

type PolicyUpdateInput struct {
	Principal                     appsession.Principal
	Name, IfMatch, IdempotencyKey string
	Patch                         map[string]any
}

type PolicyUpdateOutput struct {
	Name        string
	Version     int64
	Status      string
	EffectiveAt time.Time
}

func (s *Service) UpdatePolicy(ctx context.Context, input PolicyUpdateInput) (PolicyUpdateOutput, error) {
	if err := s.authorize(ctx, input.Principal, true, "auth.policy.write"); err != nil {
		return PolicyUpdateOutput{}, err
	}
	name := dbPolicyName(input.Name)
	if name == "" {
		return PolicyUpdateOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "정책 변경 요청이 올바르지 않습니다.")
	}
	if _, err := uuid.Parse(input.IdempotencyKey); err != nil {
		return PolicyUpdateOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "Idempotency-Key는 UUID여야 합니다.")
	}
	reason, _ := input.Patch["changeReason"].(string)
	bodyName, _ := input.Patch["policyName"].(string)
	if dbPolicyName(bodyName) != name || strings.TrimSpace(reason) == "" {
		return PolicyUpdateOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "정책 이름과 변경 사유를 확인해주세요.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	requestBody, err := json.Marshal(input.Patch)
	if err != nil {
		return PolicyUpdateOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "정책 변경 요청이 올바르지 않습니다.")
	}
	scopeHash := s.keys.Hash("operator_policy_update", input.Principal.UserID.String())
	keyHash := s.keys.Hash(input.IdempotencyKey)
	requestHash := s.keys.Hash(name, string(requestBody))
	candidate := idempotency.NewRecord("operator_policy_update", scopeHash, keyHash, requestHash, nil, nil, time.Now().UTC().Add(time.Hour))
	record, claimed, recordErr := s.idempotency.ClaimProcessing(ctx, tx, candidate, "PolicyGlobalSnapshot")
	if recordErr != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if !claimed {
		if !hmac.Equal(record.RequestHash, requestHash) {
			return PolicyUpdateOutput{}, application.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
		if record.Status != "completed" || record.ReplayID == nil {
			return PolicyUpdateOutput{}, application.Unavailable()
		}
		output, err := s.replayPolicyUpdate(ctx, tx, *record.ReplayID, input)
		if err != nil {
			return PolicyUpdateOutput{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return PolicyUpdateOutput{}, application.Unavailable()
		}
		return output, nil
	}
	global, err := s.policies.FindGlobalActiveForUpdate(ctx, tx)
	if errors.Is(err, policy.ErrNotFound) {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	active, err := s.policies.ListActiveForUpdate(ctx, tx)
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	expected, err := parseETag(input.IfMatch)
	if err != nil || expected != global.Version {
		return PolicyUpdateOutput{}, application.Problem(412, "AUTH_POLICY_PRECONDITION_FAILED", "정책 version이 현재 상태와 다릅니다.")
	}
	var previous policy.Snapshot
	for _, snapshot := range active {
		if snapshot.Name == name {
			previous = snapshot
			break
		}
	}
	if previous.Name == "" {
		return PolicyUpdateOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "알 수 없는 정책입니다.")
	}
	raw, err := patchPolicyRules(name, previous.Rules, input.Patch)
	if err != nil {
		return PolicyUpdateOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "정책 값이 계약을 만족하지 않습니다.")
	}
	updated, err := s.policies.SupersedeAndInsert(ctx, tx, previous, raw, reason, input.Principal.UserID)
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	for index := range active {
		if active[index].Name == name {
			active[index] = updated
			break
		}
	}
	view, err := policyView(active)
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	document, err := view.document()
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	global, err = s.policies.ActivateGlobal(ctx, tx, document, input.Principal.UserID, reason)
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	output := PolicyUpdateOutput{Name: publicPolicyName(updated.Name), Version: global.Version, Status: global.Status, EffectiveAt: global.EffectiveAt}
	ciphertext, err := s.keys.Seal(output)
	if err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	replayID := uuid.New()
	if err := s.idempotency.CreateReplayPayload(ctx, tx, idempotency.ReplayPayload{ID: replayID, Kind: "operator_policy_update", Ciphertext: ciphertext, BindingHash: s.policyReplayBinding(input), ExpiresAt: record.ExpiresAt}); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if err := s.idempotency.AttachReplayPayload(ctx, tx, record.ID, replayID); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if err := s.idempotency.Complete(ctx, tx, record.ID, "completed"); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if err := application.AppendAudit(ctx, tx, "auth.policy.updated", "operator", input.Principal.UserID, uuid.New(), map[string]string{"policy": name}, input.IdempotencyKey); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	return output, nil
}

func (s *Service) replayPolicyUpdate(ctx context.Context, tx pgx.Tx, replayID uuid.UUID, input PolicyUpdateInput) (PolicyUpdateOutput, error) {
	payload, err := s.idempotency.FindReplayPayloadForUpdate(ctx, tx, replayID)
	if err != nil || payload.Kind != "operator_policy_update" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(time.Now().UTC()) || !hmac.Equal(payload.BindingHash, s.policyReplayBinding(input)) {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	var output PolicyUpdateOutput
	if err := s.keys.Open(payload.Ciphertext, &output); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	if err := s.idempotency.RecordReplay(ctx, tx, replayID); err != nil {
		return PolicyUpdateOutput{}, application.Unavailable()
	}
	return output, nil
}

func (s *Service) policyReplayBinding(input PolicyUpdateInput) []byte {
	return s.keys.Hash("operator_policy_update", input.Principal.UserID.String(), input.IdempotencyKey)
}

type ManualInput struct {
	Principal                                                                                 appsession.Principal
	CaseID, TargetType, TargetID, Action, ReasonCode, ApprovalID, EvidenceRef, IdempotencyKey string
	ExpectedVersion                                                                           int64
}

func (s *Service) Manual(ctx context.Context, input ManualInput) (uuid.UUID, int64, error) {
	if !validManual(input) {
		return uuid.Nil, 0, application.Problem(400, "AUTH_INPUT_INVALID", "운영 작업 요청이 올바르지 않습니다.")
	}
	if err := s.authorize(ctx, input.Principal, true, "auth.case.execute", manualPermission(input.Action)); err != nil {
		return uuid.Nil, 0, err
	}
	if s.approvals.Verify(ctx, ApprovalRequest{CaseID: input.CaseID, ApprovalID: input.ApprovalID, EvidenceRef: input.EvidenceRef, Action: input.Action, TargetType: input.TargetType, TargetID: input.TargetID, RequiredApproverRole: "platform_operator"}) != nil {
		return uuid.Nil, 0, application.Problem(409, "AUTH_APPROVAL_REQUIRED", "승인된 운영 작업이 필요합니다.")
	}
	if _, err := uuid.Parse(input.IdempotencyKey); err != nil {
		return uuid.Nil, 0, application.Problem(400, "AUTH_INPUT_INVALID", "Idempotency-Key는 UUID여야 합니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, keyHash, requestHash := s.keys.Hash("manual_auth_action", input.Principal.UserID.String()), s.keys.Hash(input.IdempotencyKey), s.keys.Hash(input.Action, input.TargetType, input.TargetID, input.ReasonCode)
	record, err := s.idempotency.FindForUpdate(ctx, tx, "manual_auth_action", scope, keyHash)
	if err == nil {
		if !hmac.Equal(record.RequestHash, requestHash) {
			return uuid.Nil, 0, application.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
		if record.Status != "completed" || record.ResourceID == nil {
			return uuid.Nil, 0, application.Unavailable()
		}
		result, resultErr := s.users.FindManualResult(ctx, *record.ResourceID)
		if errors.Is(resultErr, operatordomain.ErrNotFound) {
			return uuid.Nil, 0, application.Unavailable()
		}
		if resultErr != nil {
			return uuid.Nil, 0, application.Unavailable()
		}
		return result.ActionID, result.TargetVersion, nil
	} else if !errors.Is(err, idempotency.ErrNotFound) {
		return uuid.Nil, 0, application.Unavailable()
	}
	actionID := uuid.New()
	record = idempotency.NewRecord("manual_auth_action", scope, keyHash, requestHash, &actionID, nil, time.Now().UTC().Add(time.Hour))
	if err := s.idempotency.CreateProcessing(ctx, tx, record, "ManualAction"); err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	version, err := s.users.ApplyManual(ctx, tx, operatordomain.ManualAction{ID: actionID, OperatorID: input.Principal.UserID, CaseID: input.CaseID, TargetType: input.TargetType, TargetID: input.TargetID, Action: input.Action, ReasonCode: input.ReasonCode, ApprovalID: input.ApprovalID, EvidenceRef: input.EvidenceRef, ExpectedVersion: input.ExpectedVersion, IdempotencyID: &record.ID})
	if errors.Is(err, operatordomain.ErrNotFound) {
		return uuid.Nil, 0, application.Problem(412, "AUTH_RESOURCE_PRECONDITION_FAILED", "대상 version이 현재 상태와 다릅니다.")
	}
	if err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	if err := s.idempotency.Complete(ctx, tx, record.ID, "completed"); err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	if err := s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.ManualActionCompleted", AggregateType: "ManualAction", AggregateID: actionID, Version: version, Payload: json.RawMessage(`{"status":"completed"}`), CorrelationID: input.Principal.SessionID}); err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	if err := application.AppendAudit(ctx, tx, "auth.manual_action.completed", "operator", input.Principal.UserID, actionID, map[string]string{"action": input.Action}, input.IdempotencyKey); err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, application.Unavailable()
	}
	return actionID, version, nil
}
func (s *Service) authorize(ctx context.Context, principal appsession.Principal, reauthRequired bool, requiredPermissions ...string) error {
	if !principal.Authenticated || principal.Channel != "web" {
		return application.Problem(403, "AUTH_FORBIDDEN", "운영자 웹 Session이 필요합니다.")
	}
	if !strongSession(principal, s.strongTTL) {
		if reauthRequired {
			return application.Problem(403, "AUTH_REAUTH_REQUIRED", "최근 강한 인증이 필요합니다.")
		}
		return application.Problem(403, "AUTH_FORBIDDEN", "최근 강한 인증이 필요합니다.")
	}
	state, grant, err := s.access.FindActive(ctx, principal.UserID)
	if errors.Is(err, access.ErrNotFound) || state.Status != "active" || grant.Status != "active" || grant.Version != principal.GrantVersion || !operatorRole(grant.Roles) {
		return application.Problem(403, "AUTH_FORBIDDEN", "현재 운영자 권한이 필요합니다.")
	}
	if err != nil {
		return application.Unavailable()
	}
	for _, required := range requiredPermissions {
		if !hasPermission(grant.Permissions, required) {
			return application.Problem(403, "AUTH_FORBIDDEN", "필요한 운영 권한이 없습니다.")
		}
	}
	return nil
}

func strongSession(principal appsession.Principal, ttl time.Duration) bool {
	if principal.Method != "email_password" && principal.Method != "passkey" {
		return false
	}
	if principal.AuthenticatedAt.IsZero() || principal.AuthenticatedAt.After(time.Now().UTC()) {
		return false
	}
	return time.Since(principal.AuthenticatedAt) <= ttl
}

func operatorRole(roles []string) bool {
	for _, role := range roles {
		if role == "platform_operator" || role == "operator" {
			return true
		}
	}
	return false
}

func hasPermission(permissions []string, required string) bool {
	for _, permission := range permissions {
		if permission == required {
			return true
		}
	}
	return false
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

var auditReasonCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

func validAuditReason(value string) bool {
	return len(value) <= 64 && auditReasonCode.MatchString(value)
}
func dbPolicyName(value string) string {
	switch value {
	case "login-lock", "login_lock":
		return "login_lock"
	case "session-ttl", "session_ttl":
		return "session_ttl"
	case "refresh-rotation", "refresh_rotation":
		return "refresh_rotation"
	case "verification", "verification_rules":
		return "verification_rules"
	case "session-revocation", "session_revocation_rules":
		return "session_revocation_rules"
	default:
		return ""
	}
}
func parseETag(value string) (int64, error) {
	value = strings.Trim(strings.TrimSpace(value), "\"")
	if !strings.HasPrefix(value, "policy-") {
		return 0, errors.New("invalid policy etag")
	}
	value = strings.TrimPrefix(value, "policy-")
	return strconv.ParseInt(value, 10, 64)
}
func validManual(v ManualInput) bool {
	if v.CaseID == "" || v.TargetID == "" || v.ReasonCode == "" || v.ApprovalID == "" || v.EvidenceRef == "" || v.ExpectedVersion < 0 {
		return false
	}
	return (v.Action == "unlock_identity" && v.TargetType == "identity") || ((v.Action == "revoke_identity_link" || v.Action == "approve_relink") && v.TargetType == "identity_link") || (v.Action == "revoke_sessions" && v.TargetType == "session")
}
