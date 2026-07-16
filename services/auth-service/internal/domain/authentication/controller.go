package authentication

import (
	"net/http"

	httpcredential "github.com/Medikong/services/services/auth-service/internal/transport/credential"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/go-chi/chi/v5"
)

type SignInController struct {
	credentials *httpcredential.Credentials
	csrf        *httputil.CSRF
	email       *EmailService
	phone       *PhoneService
}

func NewSignIn(credentials *httpcredential.Credentials, csrf *httputil.CSRF, email *EmailService, phone *PhoneService) *SignInController {
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
	result, err := c.phone.Issue(r.Context(), PhoneIssueInput{IntentID: request.AuthIntentID, OwnerProof: credential.Token, CSRFToken: csrf, Phone: request.Phone.CountryCode + request.Phone.NationalNumber, RememberMe: request.RememberMe, IdempotencyKey: r.Header.Get("Idempotency-Key")})
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
	issued, err := c.phone.Verify(r.Context(), PhoneVerifyInput{IntentID: credential.IntentID, ChallengeID: chi.URLParam(r, "challengeId"), OwnerProof: credential.Token, CSRFToken: csrf, Code: request.Code, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	c.writeIssued(w, r, issued)
}

func (c *SignInController) preAuth(w http.ResponseWriter, r *http.Request) (httpcredential.PreAuth, string, bool) {
	credential, err := c.credentials.PreAuth(r)
	if err != nil {
		httputil.WriteCredentialError(w, r, err)
		return httpcredential.PreAuth{}, "", false
	}
	if credential.Channel != httpcredential.Web {
		return credential, "", true
	}
	csrf, problem := c.csrf.Token(r)
	if problem != nil {
		httputil.WriteError(w, r, problem)
		return httpcredential.PreAuth{}, "", false
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
	if credential.Channel == httpcredential.Web {
		var problem error
		csrf, problem = c.csrf.Token(r)
		if problem != nil {
			httputil.WriteError(w, r, problem)
			return
		}
	}
	issued, err := c.email.SignIn(r.Context(), EmailInput{
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

func (c *SignInController) writeIssued(w http.ResponseWriter, r *http.Request, issued Completed) {
	if issued.WebCookie != "" {
		c.credentials.SetSessionCookie(w, issued.WebCookie, httpcredential.CookieMaxAge(issued.RememberMe, issued.RefreshTokenExpiresAt))
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
