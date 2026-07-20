package identity

import (
	"net/http"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationidentity "github.com/Medikong/services/services/auth-service/internal/application/identity"
	applicationreauth "github.com/Medikong/services/services/auth-service/internal/application/reauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
	"github.com/go-chi/chi/v5"
)

type IdentityManagementController struct {
	credentials *httpauth.Credentials
	csrf        *httputil.CSRF
	session     applicationidentity.SessionAuthenticator
	reauth      *applicationreauth.Service
	links       *applicationidentity.Service
}

func NewIdentityManagement(credentials *httpauth.Credentials, csrf *httputil.CSRF, session applicationidentity.SessionAuthenticator, reauth *applicationreauth.Service, links *applicationidentity.Service) *IdentityManagementController {
	return &IdentityManagementController{credentials: credentials, csrf: csrf, session: session, reauth: reauth, links: links}
}

type phoneRequest struct {
	CountryCode    string `json:"countryCode"`
	NationalNumber string `json:"nationalNumber"`
}

type reauthRequest struct {
	Purpose  string `json:"purpose"`
	Password string `json:"password"`
}

func (c *IdentityManagementController) Reauthenticate(w http.ResponseWriter, r *http.Request) {
	var request reauthRequest
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
	requestContext, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.reauth.Reauthenticate(r.Context(), applicationreauth.Input{Principal: requestContext.Principal, Purpose: request.Purpose, Password: request.Password, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
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
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	if request.Method != "phone" {
		httputil.WriteError(w, r, failure.Invalid("AUTH_INPUT_INVALID", "현재 지원하는 연동 수단은 휴대폰입니다."))
		return
	}
	requestContext, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.StartLink(r.Context(), applicationidentity.StartLinkInput{Principal: requestContext.Principal, Phone: request.Destination.CountryCode + request.Destination.NationalNumber, Proof: request.ReauthenticationProof, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	if result.Existing {
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{"identityLinkId": result.LinkID, "status": "active", "method": "phone"})
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{"linkIntentId": result.LinkID, "status": "requested", "method": "phone", "expiresAt": result.ExpiresAt})
}

type smsRequest struct {
	Channel string `json:"channel"`
}

func (c *IdentityManagementController) IssueLink(w http.ResponseWriter, r *http.Request) {
	var request smsRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	if request.Channel != "sms" {
		httputil.WriteError(w, r, failure.Invalid("AUTH_INPUT_INVALID", "SMS channel이 필요합니다."))
		return
	}
	requestContext, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.IssueIdentityLink(r.Context(), applicationidentity.IssueLinkInput{Principal: requestContext.Principal, LinkID: chi.URLParam(r, "linkIntentId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{"challengeId": result.ChallengeID, "maskedDestination": result.Masked, "expiresAt": result.ExpiresAt})
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
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	if request.Proof.Type != "code" {
		httputil.WriteError(w, r, failure.Invalid("AUTH_INPUT_INVALID", "code proof가 필요합니다."))
		return
	}
	requestContext, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.CompleteIdentityLink(r.Context(), applicationidentity.CompleteLinkInput{Principal: requestContext.Principal, LinkID: chi.URLParam(r, "linkIntentId"), ChallengeID: request.ChallengeID, Code: request.Proof.Value, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{"identityLinkId": result.LinkID, "status": "active", "method": "phone"})
}

type replacementStartRequest struct {
	NewPhone              phoneRequest `json:"newPhone"`
	ReauthenticationProof string       `json:"reauthenticationProof"`
}

func (c *IdentityManagementController) StartReplacement(w http.ResponseWriter, r *http.Request) {
	var request replacementStartRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	requestContext, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.StartReplacement(r.Context(), applicationidentity.ReplacementInput{Principal: requestContext.Principal, Phone: request.NewPhone.CountryCode + request.NewPhone.NationalNumber, Proof: request.ReauthenticationProof, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{"replacementId": result.LinkID, "status": "requested", "expiresAt": result.ExpiresAt})
}
func (c *IdentityManagementController) IssueReplacement(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	requestContext, _, ok := c.principal(w, r)
	if !ok {
		return
	}
	result, err := c.links.IssuePhoneReplacement(r.Context(), applicationidentity.IssueLinkInput{Principal: requestContext.Principal, LinkID: chi.URLParam(r, "replacementId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{"challengeId": result.ChallengeID, "maskedDestination": result.Masked, "expiresAt": result.ExpiresAt, "resendAvailableAt": time.Now().UTC().Add(time.Minute)})
}
func (c *IdentityManagementController) CompleteReplacement(w http.ResponseWriter, r *http.Request) {
	var request completeLinkRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	if request.Proof.Type != "code" {
		httputil.WriteError(w, r, failure.Invalid("AUTH_INPUT_INVALID", "code proof가 필요합니다."))
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
	principal, err := c.session.Authenticate(r.Context(), "", credential.Token)
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	result, err := c.links.CompletePhoneReplacement(r.Context(), applicationidentity.CompleteLinkInput{Principal: principal, LinkID: chi.URLParam(r, "replacementId"), ChallengeID: request.ChallengeID, Code: request.Proof.Value, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	c.writeReplacementCompletion(w, r, result)
}

func (c *IdentityManagementController) writeReplacementCompletion(w http.ResponseWriter, r *http.Request, result applicationidentity.CompleteLinkOutput) {
	if result.Issued.WebCookie != "" {
		c.credentials.SetSessionCookie(w, result.Issued.WebCookie, httpauth.CookieMaxAge(result.Issued.RememberMe, result.Issued.RefreshTokenExpiresAt))
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
			"replacementId": result.LinkID, "status": "active", "credentialDelivery": "web_jwt_refresh_cookie",
			"session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt},
			"access":  map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt},
		})
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{"replacementId": result.LinkID, "status": "active", "credentialDelivery": "mobile_tokens", "session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt}, "tokens": map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt, "refreshToken": result.Issued.RefreshToken, "refreshTokenExpiresAt": result.Issued.RefreshTokenExpiresAt}})
}
func (c *IdentityManagementController) principal(w http.ResponseWriter, r *http.Request) (applicationidentity.RequestContext, httpauth.Session, bool) {
	credential, credentialErr := c.credentials.Session(r)
	if credentialErr != nil || credential.Channel != httpauth.Mobile {
		if credentialErr == nil {
			credentialErr = &httpauth.Error{Kind: httpauth.Rejected}
		}
		httputil.WriteCredentialError(w, r, credentialErr)
		return applicationidentity.RequestContext{}, httpauth.Session{}, false
	}
	principal, err := c.session.Authenticate(r.Context(), "", credential.Token)
	if err != nil {
		httputil.WriteError(w, r, err)
		return applicationidentity.RequestContext{}, httpauth.Session{}, false
	}
	return applicationidentity.RequestContext{Principal: principal}, credential, true
}
func (c *IdentityManagementController) writeReauth(w http.ResponseWriter, r *http.Request, result applicationreauth.Output) {
	if result.Issued.WebCookie != "" {
		c.credentials.SetSessionCookie(w, result.Issued.WebCookie, httpauth.CookieMaxAge(result.Issued.RememberMe, result.Issued.RefreshTokenExpiresAt))
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
			"reauthenticationProof": result.Proof, "purpose": result.Purpose, "expiresAt": result.ExpiresAt, "credentialDelivery": "web_jwt_refresh_cookie",
			"session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt},
			"access":  map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt},
		})
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{"reauthenticationProof": result.Proof, "purpose": result.Purpose, "expiresAt": result.ExpiresAt, "credentialDelivery": "mobile_tokens", "session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt}, "tokens": map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt, "refreshToken": result.Issued.RefreshToken, "refreshTokenExpiresAt": result.Issued.RefreshTokenExpiresAt}})
}
