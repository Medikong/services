package authentication

import (
	"net/http"

	applicationauth "github.com/Medikong/services/services/auth-service/internal/application/authentication"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
	"github.com/go-chi/chi/v5"
)

type SignInController struct {
	credentials *httpauth.Credentials
	csrf        *httputil.CSRF
	email       *applicationauth.EmailService
	phone       *applicationauth.PhoneService
}

func NewSignIn(credentials *httpauth.Credentials, csrf *httputil.CSRF, email *applicationauth.EmailService, phone *applicationauth.PhoneService) *SignInController {
	return &SignInController{credentials: credentials, csrf: csrf, email: email, phone: phone}
}

type phoneRequest struct {
	CountryCode    string `json:"countryCode"`
	NationalNumber string `json:"nationalNumber"`
}

type phoneSignInIssueRequest struct {
	AuthIntentID string       `json:"authIntentId"`
	Phone        phoneRequest `json:"phone"`
	RememberMe   bool         `json:"rememberMe"`
}

func (c *SignInController) PhoneIssue(w http.ResponseWriter, r *http.Request) {
	var request phoneSignInIssueRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.phone.Issue(r.Context(), applicationauth.PhoneIssueInput{IntentID: request.AuthIntentID, OwnerProof: credential.Token, CSRFToken: csrf, Phone: request.Phone.CountryCode + request.Phone.NationalNumber, RememberMe: request.RememberMe, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "accepted", "challengeId": result.ChallengeID, "expiresAt": result.ExpiresAt})
}

type phoneSignInVerifyRequest struct {
	Code string `json:"code"`
}

func (c *SignInController) PhoneVerify(w http.ResponseWriter, r *http.Request) {
	var request phoneSignInVerifyRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	issued, err := c.phone.Verify(r.Context(), applicationauth.PhoneVerifyInput{IntentID: credential.IntentID, ChallengeID: chi.URLParam(r, "challengeId"), OwnerProof: credential.Token, CSRFToken: csrf, Code: request.Code, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	c.writeIssued(w, r, issued)
}

func (c *SignInController) preAuth(w http.ResponseWriter, r *http.Request) (httpauth.PreAuth, string, bool) {
	credential, err := c.credentials.PreAuth(r)
	if err != nil {
		httputil.WriteCredentialError(w, r, err)
		return httpauth.PreAuth{}, "", false
	}
	if credential.Channel != httpauth.Web {
		return credential, "", true
	}
	csrf, problem := c.csrf.Token(r)
	if problem != nil {
		httputil.WriteError(w, r, problem)
		return httpauth.PreAuth{}, "", false
	}
	return credential, csrf, true
}

type emailSignInRequest struct {
	AuthIntentID string `json:"authIntentId"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	RememberMe   bool   `json:"rememberMe"`
}

func (c *SignInController) Email(w http.ResponseWriter, r *http.Request) {
	var request emailSignInRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, credentialErr := c.credentials.PreAuth(r)
	if credentialErr != nil {
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	csrf := ""
	if credential.Channel == httpauth.Web {
		var problem error
		csrf, problem = c.csrf.Token(r)
		if problem != nil {
			httputil.WriteError(w, r, problem)
			return
		}
	}
	issued, err := c.email.SignIn(r.Context(), applicationauth.EmailInput{
		IntentID:       request.AuthIntentID,
		OwnerProof:     credential.Token,
		CSRFToken:      csrf,
		Email:          request.Email,
		Password:       request.Password,
		RememberMe:     request.RememberMe,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	c.writeIssued(w, r, issued)
}

func (c *SignInController) writeIssued(w http.ResponseWriter, r *http.Request, issued applicationauth.Completed) {
	if issued.WebCookie != "" {
		c.credentials.SetSessionCookie(w, issued.WebCookie, httpauth.CookieMaxAge(issued.RememberMe, issued.RefreshTokenExpiresAt))
		c.credentials.ClearAuthFlowCookie(w)
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
			"credentialDelivery": "web_jwt_refresh_cookie",
			"userId":             issued.UserID,
			"session":            map[string]any{"sessionId": issued.SessionID, "expiresAt": issued.ExpiresAt},
			"access":             map[string]any{"accessToken": issued.AccessToken, "accessTokenExpiresAt": issued.AccessTokenExpiresAt},
			"next":               map[string]any{"path": issued.NextPath, "intentId": issued.IntentID},
		})
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"userId":             issued.UserID,
		"credentialDelivery": "mobile_tokens",
		"session":            map[string]any{"sessionId": issued.SessionID, "expiresAt": issued.ExpiresAt},
		"tokens": map[string]any{
			"accessToken": issued.AccessToken, "accessTokenExpiresAt": issued.AccessTokenExpiresAt,
			"refreshToken": issued.RefreshToken, "refreshTokenExpiresAt": issued.RefreshTokenExpiresAt,
		},
		"next": map[string]any{"path": issued.NextPath, "intentId": issued.IntentID},
	})
}
