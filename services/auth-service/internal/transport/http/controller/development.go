package controller

import (
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/application"
	"github.com/Medikong/services/services/auth-service/internal/application/development"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type DevelopmentController struct {
	contract httpcontract.Contract
	virtual  *development.VirtualMessageService
	sessions *appsession.Service
}

func NewDevelopment(contract httpcontract.Contract, virtual *development.VirtualMessageService, sessions *appsession.Service) *DevelopmentController {
	return &DevelopmentController{contract: contract, virtual: virtual, sessions: sessions}
}

func (c *DevelopmentController) VirtualMessage(w http.ResponseWriter, r *http.Request) {
	if credentialErr := c.contract.DevelopmentAccessToken(r); credentialErr != nil {
		writeApplicationError(w, r, application.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	ownerProof, csrf := "", ""
	preAuth, preAuthErr := c.contract.PreAuthCredential(r)
	if preAuthErr != nil && preAuthErr.Kind != httpcontract.CredentialMissing {
		writeApplicationError(w, r, application.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	if preAuthErr == nil {
		ownerProof = preAuth.Token
	}
	if preAuthErr == nil && preAuth.Channel == httpcontract.CredentialChannelWeb {
		// GET has no CSRF requirement; passing the optional header lets the
		// application verify it if a future challenge kind requires it.
		csrf = r.Header.Get("X-CSRF-Token")
	}
	var sessionUserID *uuid.UUID
	sessionCredential, sessionCredentialErr := c.contract.SessionCredential(r)
	if sessionCredentialErr != nil && sessionCredentialErr.Kind != httpcontract.CredentialMissing {
		writeApplicationError(w, r, application.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	if sessionCredentialErr == nil {
		principal, err := c.sessions.Authenticate(r.Context(), tokenForWeb(sessionCredential), tokenForMobile(sessionCredential))
		if err != nil || !principal.Authenticated {
			writeApplicationError(w, r, application.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
			return
		}
		sessionUserID = &principal.UserID
	}
	if preAuthErr != nil && sessionCredentialErr != nil {
		writeApplicationError(w, r, application.Problem(404, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND", "가상 인증 메시지를 찾을 수 없습니다."))
		return
	}
	result, err := c.virtual.Get(r.Context(), development.VirtualMessageInput{ChallengeID: chi.URLParam(r, "challengeId"), OwnerProof: ownerProof, CSRFToken: csrf, SessionUser: sessionUserID})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"challengeId": result.ChallengeID, "channel": result.Channel, "status": result.Status,
		"code": result.Code, "maskedDestination": result.MaskedDestination, "expiresAt": result.ExpiresAt,
	})
}
