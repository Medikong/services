package intent

import (
	"net/http"

	applicationintent "github.com/Medikong/services/services/auth-service/internal/application/intent"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
	"github.com/go-chi/chi/v5"
)

type ActionResumeController struct {
	credentials *httpauth.Credentials
	sessions    *applicationsession.Service
	service     *applicationintent.ActionResumeService
}

func NewActionResume(credentials *httpauth.Credentials, sessions *applicationsession.Service, service *applicationintent.ActionResumeService) *ActionResumeController {
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
	result, err := c.service.Resume(r.Context(), applicationintent.ResumeInput{
		Principal: applicationintent.Principal{Authenticated: principal.Authenticated, SessionID: principal.SessionID, UserID: principal.UserID},
		IntentID:  chi.URLParam(r, "intentId"), IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{"intentId": result.IntentID, "action": result.Action, "actionContext": result.ActionContext, "returnPath": result.ReturnPath})
}
