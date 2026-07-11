package controller

import (
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/application/actionresume"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
)

type ActionResumeController struct {
	contract httpcontract.Contract
	sessions *appsession.Service
	service  *actionresume.Service
}

func NewActionResume(contract httpcontract.Contract, sessions *appsession.Service, service *actionresume.Service) *ActionResumeController {
	return &ActionResumeController{contract: contract, sessions: sessions, service: service}
}
func (c *ActionResumeController) Resume(w http.ResponseWriter, r *http.Request) {
	var request emptyRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil {
		writeCredentialError(w, r, credentialErr)
		return
	}
	principal, err := c.sessions.Authenticate(r.Context(), tokenForWeb(credential), tokenForMobile(credential))
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	if credential.Channel == httpcontract.CredentialChannelWeb {
		csrf, problem := c.contract.WebCSRFToken(r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		if err := c.sessions.VerifyWebCSRF(r.Context(), credential.Token, csrf); err != nil {
			writeApplicationError(w, r, err)
			return
		}
	}
	result, err := c.service.Resume(r.Context(), actionresume.Input{Principal: principal, IntentID: chi.URLParam(r, "intentId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"intentId": result.IntentID, "action": result.Action, "actionContext": result.ActionContext, "returnPath": result.ReturnPath})
}
