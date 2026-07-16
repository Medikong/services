package userauthstate

import (
	"context"
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	httpcredential "github.com/Medikong/services/services/auth-service/internal/transport/credential"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/go-chi/chi/v5"
)

type SessionAuthenticator interface {
	Authenticate(context.Context, string, string) (domain.Principal, error)
}

type UserAuthStateController struct {
	credentials *httpcredential.Credentials
	sessions    SessionAuthenticator
	service     *Service
}

func NewUserAuthState(credentials *httpcredential.Credentials, sessions SessionAuthenticator, service *Service) *UserAuthStateController {
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
	if credentialErr != nil || credential.Channel != httpcredential.Mobile {
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	principal, err := c.sessions.Authenticate(r.Context(), "", credential.Token)
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	output, err := c.service.Apply(r.Context(), ApplyInput{
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
