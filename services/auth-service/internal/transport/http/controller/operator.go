package controller

import (
	"fmt"
	"net/http"

	appoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
)

type OperatorController struct {
	contract httpcontract.Contract
	sessions *appsession.Service
	service  *appoperator.Service
}

func NewOperator(contract httpcontract.Contract, sessions *appsession.Service, service *appoperator.Service) *OperatorController {
	return &OperatorController{contract: contract, sessions: sessions, service: service}
}
func (c *OperatorController) User(w http.ResponseWriter, r *http.Request) {
	principal, ok := c.principal(w, r, false)
	if !ok {
		return
	}
	view, err := c.service.User(r.Context(), principal, chi.URLParam(r, "userId"), r.Header.Get("X-Audit-Reason-Code"), httpcontract.EnsureRequestID(r))
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	identities := make([]map[string]any, 0, len(view.Identities))
	for _, item := range view.Identities {
		lock := map[string]any{"locked": item.Locked}
		if item.UnlockAvailableAt != nil {
			lock["unlockAvailableAt"] = *item.UnlockAvailableAt
		}
		identities = append(identities, map[string]any{"identityId": item.IdentityID.String(), "identityLinkId": item.LinkID.String(), "type": item.Type, "maskedValue": item.MaskedValue, "verificationStatus": item.VerificationStatus, "linkStatus": item.LinkStatus, "rowVersion": item.RowVersion, "lock": lock})
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"userId": view.UserID.String(), "status": view.Status, "userAuthStateVersion": view.Version, "identities": identities, "activeSessionCount": view.ActiveSessions})
}
func (c *OperatorController) Policies(w http.ResponseWriter, r *http.Request) {
	principal, ok := c.principal(w, r, false)
	if !ok {
		return
	}
	view, err := c.service.PolicyView(r.Context(), principal)
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", policyETag(view.Version))
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"version": view.Version, "status": view.Status, "effectiveAt": view.EffectiveAt,
		"loginLock": view.LoginLock, "sessionTtl": view.SessionTTL, "refreshRotation": view.RefreshRotation,
		"verificationRules": view.VerificationRules, "sessionRevocationRules": view.SessionRevocationRules,
	})
}
func (c *OperatorController) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	principal, ok := c.principal(w, r, true)
	if !ok {
		return
	}
	updated, err := c.service.UpdatePolicy(r.Context(), appoperator.PolicyUpdateInput{Principal: principal, Name: chi.URLParam(r, "policyName"), IfMatch: r.Header.Get("If-Match"), IdempotencyKey: r.Header.Get("Idempotency-Key"), Patch: request})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", policyETag(updated.Version))
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"name": updated.Name, "version": updated.Version, "status": updated.Status, "effectiveAt": updated.EffectiveAt})
}

func policyETag(version int64) string { return fmt.Sprintf("\"policy-%d\"", version) }

type manualTargetRequest struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type manualActionRequest struct {
	CaseID                string              `json:"caseId"`
	Target                manualTargetRequest `json:"target"`
	Action                string              `json:"action"`
	ReasonCode            string              `json:"reasonCode"`
	ApprovalID            string              `json:"approvalId"`
	EvidenceRef           string              `json:"evidenceRef"`
	ExpectedTargetVersion int64               `json:"expectedTargetVersion"`
}

func (c *OperatorController) Manual(w http.ResponseWriter, r *http.Request) {
	var request manualActionRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	principal, ok := c.principal(w, r, true)
	if !ok {
		return
	}
	actionID, version, err := c.service.Manual(r.Context(), appoperator.ManualInput{Principal: principal, CaseID: request.CaseID, TargetType: request.Target.Type, TargetID: request.Target.ID, Action: request.Action, ReasonCode: request.ReasonCode, ApprovalID: request.ApprovalID, EvidenceRef: request.EvidenceRef, ExpectedVersion: request.ExpectedTargetVersion, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"actionId": actionID.String(), "action": request.Action, "status": "completed", "targetVersion": version})
}
func (c *OperatorController) principal(w http.ResponseWriter, r *http.Request, requireCSRF bool) (appsession.Principal, bool) {
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil || credential.Channel != httpcontract.CredentialChannelWeb {
		writeCredentialError(w, r, credentialErr)
		return appsession.Principal{}, false
	}
	if requireCSRF {
		csrf, problem := c.contract.WebCSRFToken(r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return appsession.Principal{}, false
		}
		if err := c.sessions.VerifyWebCSRF(r.Context(), credential.Token, csrf); err != nil {
			writeApplicationError(w, r, err)
			return appsession.Principal{}, false
		}
	}
	principal, err := c.sessions.Authenticate(r.Context(), credential.Token, "")
	if err != nil {
		writeApplicationError(w, r, err)
		return appsession.Principal{}, false
	}
	return principal, true
}
