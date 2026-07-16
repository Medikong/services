package intent

import (
	"net/http"

	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	httpauth "github.com/Medikong/services/services/auth-service/internal/platform/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/go-chi/chi/v5"
)

type ActionResumeController struct {
	credentials *httpauth.Credentials
	sessions    *appsession.Service
	service     *ActionResumeService
}

func NewActionResume(credentials *httpauth.Credentials, sessions *appsession.Service, service *ActionResumeService) *ActionResumeController {
	return &ActionResumeController{credentials: credentials, sessions: sessions, service: service}
}
func (c *ActionResumeController) Resume(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, credentialErr := c.credentials.Session(r)
	if credentialErr != nil || credential.Channel != httpauth.Mobile {
		if credentialErr == nil {
			credentialErr = &httpauth.Error{Kind: httpauth.Rejected}
		}
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	principal, err := c.sessions.Authenticate(r.Context(), "", credential.Token)
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	result, err := c.service.Resume(r.Context(), Input{Principal: principal, IntentID: chi.URLParam(r, "intentId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{"intentId": result.IntentID, "action": result.Action, "actionContext": result.ActionContext, "returnPath": result.ReturnPath})
}
