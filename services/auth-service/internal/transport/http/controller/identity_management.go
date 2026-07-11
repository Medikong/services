package controller

import (
	"net/http"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	appidentity "github.com/Medikong/services/services/auth-service/internal/application/identitymanagement"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
)

type IdentityManagementController struct {
	contract httpcontract.Contract
	session  *appsession.Service
	reauth   *appidentity.ReauthService
	links    *appidentity.LinkService
}

func NewIdentityManagement(contract httpcontract.Contract, session *appsession.Service, reauth *appidentity.ReauthService, links *appidentity.LinkService) *IdentityManagementController {
	return &IdentityManagementController{contract: contract, session: session, reauth: reauth, links: links}
}

type reauthRequest struct {
	Purpose  string `json:"purpose"`
	Password string `json:"password"`
}

func (c *IdentityManagementController) Reauthenticate(w http.ResponseWriter, r *http.Request) {
	var request reauthRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil {
		writeCredentialError(w, r, credentialErr)
		return
	}
	csrf := ""
	if credential.Channel == httpcontract.CredentialChannelWeb {
		var problem *httpcontract.ContractError
		csrf, problem = c.contract.WebCSRFToken(r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
	}
	principal, err := c.session.Authenticate(r.Context(), tokenForWeb(credential), tokenForMobile(credential))
	if err != nil {
		if credential.Channel == httpcontract.CredentialChannelWeb && application.AsError(err).Status == http.StatusUnauthorized {
			result, recoveryErr := c.reauth.RecoverWebDelivery(r.Context(), credential.Token, csrf, request.Purpose, request.Password, r.Header.Get("Idempotency-Key"))
			if recoveryErr != nil {
				writeApplicationError(w, r, recoveryErr)
				return
			}
			c.writeReauth(w, r, result)
			return
		}
		writeApplicationError(w, r, err)
		return
	}
	if credential.Channel == httpcontract.CredentialChannelWeb {
		if err := c.session.VerifyWebCSRF(r.Context(), credential.Token, csrf); err != nil {
			writeApplicationError(w, r, err)
			return
		}
	}
	previousWebCookie := ""
	if credential.Channel == httpcontract.CredentialChannelWeb {
		previousWebCookie = credential.Token
	}
	result, err := c.reauth.Reauthenticate(r.Context(), appidentity.ReauthInput{Principal: principal, Purpose: request.Purpose, Password: request.Password, IdempotencyKey: r.Header.Get("Idempotency-Key"), PreviousWebCookie: previousWebCookie})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	c.writeReauth(w, r, result)
}

type startLinkRequest struct {
	Method                string       `json:"method"`
	Destination           phoneRequest `json:"destination"`
	ReauthenticationProof string       `json:"reauthenticationProof"`
}

func (c *IdentityManagementController) StartLink(w http.ResponseWriter, r *http.Request) {
	var request startLinkRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	if request.Method != "phone" {
		writeApplicationError(w, r, application.Problem(400, "AUTH_INPUT_INVALID", "현재 지원하는 연동 수단은 휴대폰입니다."))
		return
	}
	principal, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.StartLink(r.Context(), appidentity.StartLinkInput{Principal: principal, Phone: request.Destination.CountryCode + request.Destination.NationalNumber, Proof: request.ReauthenticationProof, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	if result.Existing {
		httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"identityLinkId": result.LinkID, "status": "active", "method": "phone"})
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{"linkIntentId": result.LinkID, "status": "requested", "method": "phone", "expiresAt": result.ExpiresAt})
}

type smsRequest struct {
	Channel string `json:"channel"`
}

func (c *IdentityManagementController) IssueLink(w http.ResponseWriter, r *http.Request) {
	var request smsRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	if request.Channel != "sms" {
		writeApplicationError(w, r, application.Problem(400, "AUTH_INPUT_INVALID", "SMS channel이 필요합니다."))
		return
	}
	principal, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.IssueIdentityLink(r.Context(), appidentity.IssueLinkInput{Principal: principal, LinkID: chi.URLParam(r, "linkIntentId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{"challengeId": result.ChallengeID, "maskedDestination": result.Masked, "expiresAt": result.ExpiresAt})
}

type proofRequest struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type completeLinkRequest struct {
	ChallengeID string       `json:"challengeId"`
	Proof       proofRequest `json:"proof"`
}

func (c *IdentityManagementController) CompleteLink(w http.ResponseWriter, r *http.Request) {
	var request completeLinkRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	if request.Proof.Type != "code" {
		writeApplicationError(w, r, application.Problem(400, "AUTH_INPUT_INVALID", "code proof가 필요합니다."))
		return
	}
	principal, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.CompleteIdentityLink(r.Context(), appidentity.CompleteLinkInput{Principal: principal, LinkID: chi.URLParam(r, "linkIntentId"), ChallengeID: request.ChallengeID, Code: request.Proof.Value, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"identityLinkId": result.LinkID, "status": "active", "method": "phone"})
}

type replacementStartRequest struct {
	NewPhone              phoneRequest `json:"newPhone"`
	ReauthenticationProof string       `json:"reauthenticationProof"`
}

func (c *IdentityManagementController) StartReplacement(w http.ResponseWriter, r *http.Request) {
	var request replacementStartRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	principal, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.StartReplacement(r.Context(), appidentity.ReplacementInput{Principal: principal, Phone: request.NewPhone.CountryCode + request.NewPhone.NationalNumber, Proof: request.ReauthenticationProof, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{"replacementId": result.LinkID, "status": "requested", "expiresAt": result.ExpiresAt})
}
func (c *IdentityManagementController) IssueReplacement(w http.ResponseWriter, r *http.Request) {
	var request emptyRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	principal, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.IssuePhoneReplacement(r.Context(), appidentity.IssueLinkInput{Principal: principal, LinkID: chi.URLParam(r, "replacementId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{"challengeId": result.ChallengeID, "maskedDestination": result.Masked, "expiresAt": result.ExpiresAt, "resendAvailableAt": time.Now().UTC().Add(time.Minute)})
}
func (c *IdentityManagementController) CompleteReplacement(w http.ResponseWriter, r *http.Request) {
	var request completeLinkRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	if request.Proof.Type != "code" {
		writeApplicationError(w, r, application.Problem(400, "AUTH_INPUT_INVALID", "code proof가 필요합니다."))
		return
	}
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil {
		writeCredentialError(w, r, credentialErr)
		return
	}
	csrf := ""
	if credential.Channel == httpcontract.CredentialChannelWeb {
		var problem *httpcontract.ContractError
		csrf, problem = c.contract.WebCSRFToken(r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
	}
	principal, err := c.session.Authenticate(r.Context(), tokenForWeb(credential), tokenForMobile(credential))
	if err != nil {
		if credential.Channel == httpcontract.CredentialChannelWeb && application.AsError(err).Status == http.StatusUnauthorized {
			result, recoveryErr := c.links.RecoverPhoneReplacementWebDelivery(r.Context(), credential.Token, csrf, chi.URLParam(r, "replacementId"), request.ChallengeID, request.Proof.Value, r.Header.Get("Idempotency-Key"))
			if recoveryErr != nil {
				writeApplicationError(w, r, recoveryErr)
				return
			}
			c.writeReplacementCompletion(w, r, result)
			return
		}
		writeApplicationError(w, r, err)
		return
	}
	if credential.Channel == httpcontract.CredentialChannelWeb {
		if err := c.session.VerifyWebCSRF(r.Context(), credential.Token, csrf); err != nil {
			writeApplicationError(w, r, err)
			return
		}
	}
	previousWebCookie := ""
	if credential.Channel == httpcontract.CredentialChannelWeb {
		previousWebCookie = credential.Token
	}
	result, err := c.links.CompletePhoneReplacement(r.Context(), appidentity.CompleteLinkInput{Principal: principal, LinkID: chi.URLParam(r, "replacementId"), ChallengeID: request.ChallengeID, Code: request.Proof.Value, IdempotencyKey: r.Header.Get("Idempotency-Key"), PreviousWebCookie: previousWebCookie})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	c.writeReplacementCompletion(w, r, result)
}

func (c *IdentityManagementController) writeReplacementCompletion(w http.ResponseWriter, r *http.Request, result appidentity.CompleteLinkOutput) {
	if result.Issued.WebCookie != "" {
		c.contract.IssueSessionCookie(w, result.Issued.WebCookie, int(time.Until(result.Issued.ExpiresAt).Seconds()))
		httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"replacementId": result.LinkID, "status": "active", "credentialDelivery": "web_session", "sessionId": result.Issued.SessionID, "csrfToken": result.Issued.CSRFToken})
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"replacementId": result.LinkID, "status": "active", "credentialDelivery": "mobile_tokens", "session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt}, "tokens": map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt, "refreshToken": result.Issued.RefreshToken, "refreshTokenExpiresAt": result.Issued.RefreshTokenExpiresAt}})
}
func (c *IdentityManagementController) principal(w http.ResponseWriter, r *http.Request) (appsession.Principal, httpcontract.SessionCredential, bool) {
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil {
		writeCredentialError(w, r, credentialErr)
		return appsession.Principal{}, httpcontract.SessionCredential{}, false
	}
	principal, err := c.session.Authenticate(r.Context(), tokenForWeb(credential), tokenForMobile(credential))
	if err != nil {
		writeApplicationError(w, r, err)
		return appsession.Principal{}, httpcontract.SessionCredential{}, false
	}
	if credential.Channel == httpcontract.CredentialChannelWeb {
		csrf, problem := c.contract.WebCSRFToken(r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return appsession.Principal{}, httpcontract.SessionCredential{}, false
		}
		if err := c.session.VerifyWebCSRF(r.Context(), credential.Token, csrf); err != nil {
			writeApplicationError(w, r, err)
			return appsession.Principal{}, httpcontract.SessionCredential{}, false
		}
	}
	return principal, credential, true
}
func (c *IdentityManagementController) writeReauth(w http.ResponseWriter, r *http.Request, result appidentity.ReauthOutput) {
	if result.Issued.WebCookie != "" {
		c.contract.IssueSessionCookie(w, result.Issued.WebCookie, int(time.Until(result.Issued.ExpiresAt).Seconds()))
		httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"reauthenticationProof": result.Proof, "purpose": result.Purpose, "expiresAt": result.ExpiresAt, "credentialDelivery": "web_session", "sessionId": result.Issued.SessionID, "csrfToken": result.Issued.CSRFToken})
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"reauthenticationProof": result.Proof, "purpose": result.Purpose, "expiresAt": result.ExpiresAt, "credentialDelivery": "mobile_tokens", "session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt}, "tokens": map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt, "refreshToken": result.Issued.RefreshToken, "refreshTokenExpiresAt": result.Issued.RefreshTokenExpiresAt}})
}
