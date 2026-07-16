package registration

import (
	"net/http"
	"strings"

	httpauth "github.com/Medikong/services/services/auth-service/internal/platform/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/go-chi/chi/v5"
)

// RegistrationController handles the public HTTP shape and delegates state
// changes to the registration service.
type RegistrationController struct {
	credentials *httpauth.Credentials
	csrf        *httputil.CSRF
	service     *Service
}

func NewRegistration(credentials *httpauth.Credentials, csrf *httputil.CSRF, service *Service) *RegistrationController {
	return &RegistrationController{credentials: credentials, csrf: csrf, service: service}
}

type phoneRequest struct {
	CountryCode    string `json:"countryCode"`
	NationalNumber string `json:"nationalNumber"`
}

type startRegistrationRequest struct {
	AuthIntentID       string       `json:"authIntentId"`
	Email              string       `json:"email"`
	Password           string       `json:"password"`
	Phone              phoneRequest `json:"phone"`
	ProfileRequestID   string       `json:"profileRequestId"`
	AgreementReceiptID string       `json:"agreementReceiptId"`
	RememberMe         bool         `json:"rememberMe"`
}

func (c *RegistrationController) Start(w http.ResponseWriter, r *http.Request) {
	var request startRegistrationRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.Start(r.Context(), StartInput{
		IntentID: request.AuthIntentID, OwnerProof: credential.Token, CSRFToken: csrf,
		Email: request.Email, Password: request.Password, Phone: request.Phone.CountryCode + request.Phone.NationalNumber,
		ProfileRequestID: request.ProfileRequestID, AgreementReceiptID: request.AgreementReceiptID,
		RememberMe: request.RememberMe, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"registrationId":                   result.RegistrationID,
		"status":                           result.Status,
		"requiredVerifications":            result.RequiredVerifications,
		"verifiedMethods":                  result.VerifiedMethods,
		"expiresAt":                        result.ExpiresAt,
		"registrationStatusToken":          result.RegistrationStatusToken,
		"registrationStatusTokenExpiresAt": result.StatusTokenExpiresAt,
	})
}

type issueRegistrationChallengeRequest struct {
	Method string `json:"method"`
}

func (c *RegistrationController) IssueChallenge(w http.ResponseWriter, r *http.Request) {
	var request issueRegistrationChallengeRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.IssueChallenge(r.Context(), IssueChallengeInput{
		RegistrationID: chi.URLParam(r, "registrationId"), OwnerProof: credential.Token, CSRFToken: csrf,
		Method: request.Method, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"challengeId": result.ChallengeID, "method": result.Method, "maskedDestination": result.MaskedDestination,
		"expiresAt": result.ExpiresAt, "resendAvailableAt": result.ResendAvailableAt,
	})
}

type verifyRegistrationChallengeRequest struct {
	Code string `json:"code"`
}

func (c *RegistrationController) VerifyChallenge(w http.ResponseWriter, r *http.Request) {
	var request verifyRegistrationChallengeRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.VerifyChallenge(r.Context(), VerifyChallengeInput{
		RegistrationID: chi.URLParam(r, "registrationId"), ChallengeID: chi.URLParam(r, "challengeId"),
		OwnerProof: credential.Token, CSRFToken: csrf, Code: request.Code, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	registration := map[string]any{
		"status": result.RegistrationStatus, "verifiedMethods": result.VerifiedMethods,
		"requiredVerifications": []string{"email", "phone"},
	}
	if result.RegistrationCompletionProof != "" {
		registration["registrationCompletionProof"] = result.RegistrationCompletionProof
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"challengeId": result.ChallengeID, "status": result.Status, "registration": registration,
	})
}

type completeRegistrationRequest struct {
	UserID            string `json:"userId"`
	UserCreationProof string `json:"userCreationProof"`
}

func (c *RegistrationController) Complete(w http.ResponseWriter, r *http.Request) {
	var request completeRegistrationRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.Complete(r.Context(), CompleteInput{
		RegistrationID: chi.URLParam(r, "registrationId"), UserID: request.UserID, UserCreationProof: request.UserCreationProof,
		OwnerProof: credential.Token, CSRFToken: csrf, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	if result.Issued.WebCookie != "" {
		c.credentials.SetSessionCookie(w, result.Issued.WebCookie, httpauth.CookieMaxAge(result.Issued.RememberMe, result.Issued.RefreshTokenExpiresAt))
		c.credentials.ClearAuthFlowCookie(w)
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
			"registrationId": result.RegistrationID, "status": result.Status, "credentialDelivery": "web_jwt_refresh_cookie",
			"userId":  result.Issued.UserID,
			"session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt},
			"access":  map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt},
			"next":    map[string]any{"path": result.NextPath, "intentId": result.IntentID},
		})
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registrationId": result.RegistrationID, "status": result.Status, "credentialDelivery": "mobile_tokens",
		"userId": result.Issued.UserID, "session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt},
		"tokens": map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt, "refreshToken": result.Issued.RefreshToken, "refreshTokenExpiresAt": result.Issued.RefreshTokenExpiresAt},
		"next":   map[string]any{"path": result.NextPath, "intentId": result.IntentID},
	})
}

func (c *RegistrationController) Status(w http.ResponseWriter, r *http.Request) {
	ownerProof, csrf := "", ""
	if credential, credentialErr := c.credentials.PreAuth(r); credentialErr == nil {
		ownerProof = credential.Token
		if credential.Channel == httpauth.Web {
			// GET needs no CSRF check, but a supplied token is not required either.
			csrf = strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		}
	}
	statusToken, _ := httpauth.RegistrationStatusToken(r)
	result, err := c.service.Status(r.Context(), StatusInput{
		RegistrationID: chi.URLParam(r, "registrationId"), OwnerProof: ownerProof, CSRFToken: csrf, StatusToken: statusToken,
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registrationId": result.RegistrationID, "status": result.Status, "verifiedMethods": result.VerifiedMethods,
		"retryable": result.Retryable, "expiresAt": result.ExpiresAt,
	})
}

func (c *RegistrationController) preAuth(w http.ResponseWriter, r *http.Request) (httpauth.PreAuth, string, bool) {
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
