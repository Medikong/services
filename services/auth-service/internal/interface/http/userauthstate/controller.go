package userauthstate

import (
	"net/http"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	applicationuserauthstate "github.com/Medikong/services/services/auth-service/internal/application/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
	"github.com/go-chi/chi/v5"
)

type UserAuthStateController struct {
	credentials *httpauth.Credentials
	sessions    *applicationsession.Service
	service     *applicationuserauthstate.Service
}

func NewUserAuthState(credentials *httpauth.Credentials, sessions *applicationsession.Service, service *applicationuserauthstate.Service) *UserAuthStateController {
	return &UserAuthStateController{credentials: credentials, sessions: sessions, service: service}
}

type applyUserAccountStatusRequest struct {
	UserStatusChangeProof string `json:"userStatusChangeProof"`
}

func (c *UserAuthStateController) Apply(w http.ResponseWriter, r *http.Request) {
	var request applyUserAccountStatusRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, credentialErr := c.credentials.Session(r)
	if credentialErr != nil || credential.Channel != httpauth.Mobile {
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	principal, err := c.sessions.Authenticate(r.Context(), "", credential.Token)
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	output, err := c.service.Apply(r.Context(), applicationuserauthstate.ApplyInput{
		Principal:             principal,
		PathUserID:            chi.URLParam(r, "userId"),
		UserStatusChangeProof: request.UserStatusChangeProof,
		AuthorizationDecision: r.Header.Get("X-Authorization-Decision"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"userId": output.UserID.String(), "accountStatus": output.AccountStatus,
		"userVersion": output.UserVersion, "applied": output.Applied,
	})
}
