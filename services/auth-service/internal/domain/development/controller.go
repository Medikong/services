package development

import (
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	httpcredential "github.com/Medikong/services/services/auth-service/internal/transport/credential"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type DevelopmentController struct {
	credentials *httpcredential.Credentials
	virtual     *VirtualMessageService
	sessions    *appsession.Service
}

func NewDevelopment(credentials *httpcredential.Credentials, virtual *VirtualMessageService, sessions *appsession.Service) *DevelopmentController {
	return &DevelopmentController{credentials: credentials, virtual: virtual, sessions: sessions}
}

func (c *DevelopmentController) VirtualMessage(w http.ResponseWriter, r *http.Request) {
	if credentialErr := c.credentials.DevelopmentToken(r); credentialErr != nil {
		httputil.WriteError(w, r, domain.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	ownerProof, csrf := "", ""
	preAuth, preAuthErr := c.credentials.PreAuth(r)
	if preAuthErr != nil && preAuthErr.Kind != httpcredential.Missing {
		httputil.WriteError(w, r, domain.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	if preAuthErr == nil {
		ownerProof = preAuth.Token
	}
	if preAuthErr == nil && preAuth.Channel == httpcredential.Web {
		// GET has no CSRF requirement; passing the optional header lets the
		// application verify it if a future challenge kind requires it.
		csrf = r.Header.Get("X-CSRF-Token")
	}
	var sessionUserID *uuid.UUID
	sessionCredential, sessionCredentialErr := c.credentials.Session(r)
	if sessionCredentialErr != nil && sessionCredentialErr.Kind != httpcredential.Missing {
		httputil.WriteError(w, r, domain.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	if sessionCredentialErr == nil {
		webToken, mobileToken := "", ""
		if sessionCredential.Channel == httpcredential.Web {
			webToken = sessionCredential.Token
		} else {
			mobileToken = sessionCredential.Token
		}
		principal, err := c.sessions.Authenticate(r.Context(), webToken, mobileToken)
		if err != nil || !principal.Authenticated {
			httputil.WriteError(w, r, domain.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
			return
		}
		sessionUserID = &principal.UserID
	}
	if preAuthErr != nil && sessionCredentialErr != nil {
		httputil.WriteError(w, r, domain.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	result, err := c.virtual.Get(r.Context(), VirtualMessageInput{ChallengeID: chi.URLParam(r, "challengeId"), OwnerProof: ownerProof, CSRFToken: csrf, SessionUser: sessionUserID})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"challengeId": result.ChallengeID, "channel": result.Channel, "status": result.Status,
		"code": result.Code, "maskedDestination": result.MaskedDestination, "expiresAt": result.ExpiresAt,
	})
}
