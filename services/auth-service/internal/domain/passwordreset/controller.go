package passwordreset

import (
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	httpauth "github.com/Medikong/services/services/auth-service/internal/platform/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/go-chi/chi/v5"
)

type PasswordResetController struct {
	credentials *httpauth.Credentials
	csrf        *httputil.CSRF
	service     *Service
}

func NewPasswordReset(credentials *httpauth.Credentials, csrf *httputil.CSRF, service *Service) *PasswordResetController {
	return &PasswordResetController{credentials: credentials, csrf: csrf, service: service}
}

type phoneRequest struct {
	CountryCode    string `json:"countryCode"`
	NationalNumber string `json:"nationalNumber"`
}

type passwordResetStartRequest struct {
	IdentifierType string        `json:"identifierType"`
	Email          string        `json:"email,omitempty"`
	Phone          *phoneRequest `json:"phone,omitempty"`
}

func (c *PasswordResetController) Start(w http.ResponseWriter, r *http.Request) {
	var request passwordResetStartRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	phone := ""
	if request.Phone != nil {
		phone = request.Phone.CountryCode + request.Phone.NationalNumber
	}
	result, err := c.service.Start(r.Context(), StartInput{IntentID: credential.IntentID, OwnerProof: credential.Token, CSRFToken: csrf, IdentifierType: request.IdentifierType, Email: request.Email, Phone: phone, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusAccepted, map[string]any{"passwordResetId": result.ResetID, "status": "accepted", "methodOptions": []string{"email", "phone"}, "expiresAt": result.ExpiresAt})
}

type passwordResetIssueRequest struct {
	Method string `json:"method"`
}

func (c *PasswordResetController) Issue(w http.ResponseWriter, r *http.Request) {
	var request passwordResetIssueRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.Issue(r.Context(), IssueInput{ResetID: chi.URLParam(r, "passwordResetId"), OwnerProof: credential.Token, CSRFToken: csrf, Method: request.Method, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "accepted", "challengeId": result.ChallengeID, "expiresAt": result.ExpiresAt})
}

type passwordResetVerifyRequest struct {
	Code string `json:"code"`
}

func (c *PasswordResetController) Verify(w http.ResponseWriter, r *http.Request) {
	var request passwordResetVerifyRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	channel := string(credential.Channel)
	result, err := c.service.Verify(r.Context(), VerifyInput{ResetID: chi.URLParam(r, "passwordResetId"), ChallengeID: chi.URLParam(r, "challengeId"), OwnerProof: credential.Token, CSRFToken: csrf, Code: request.Code, Channel: channel, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	data := map[string]any{"passwordResetId": result.ResetID, "status": "verified", "expiresAt": result.ExpiresAt}
	if channel != "web" {
		data["credentialDelivery"] = "mobile_reset_grant"
		data["resetGrant"] = result.ResetGrant
	} else {
		data["credentialDelivery"] = "web_auth_flow"
	}
	httputil.WriteJSON(w, r, http.StatusOK, data)
}

type passwordResetCompleteRequest struct {
	CredentialDelivery string `json:"credentialDelivery"`
	ResetGrant         string `json:"resetGrant,omitempty"`
	NewPassword        string `json:"newPassword"`
	ConfirmPassword    string `json:"confirmPassword"`
}

func (c *PasswordResetController) Complete(w http.ResponseWriter, r *http.Request) {
	var request passwordResetCompleteRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	if (credential.Channel == httpauth.Web && request.CredentialDelivery != "web_auth_flow") || (credential.Channel == httpauth.Mobile && request.CredentialDelivery != "mobile_reset_grant") {
		httputil.WriteError(w, r, domain.Problem(400, "AUTH_INPUT_INVALID", "요청 채널과 credentialDelivery가 일치하지 않습니다."))
		return
	}
	if err := c.service.Complete(r.Context(), CompleteInput{ResetID: chi.URLParam(r, "passwordResetId"), OwnerProof: credential.Token, CSRFToken: csrf, Channel: string(credential.Channel), ResetGrant: request.ResetGrant, NewPassword: request.NewPassword, ConfirmPassword: request.ConfirmPassword, IdempotencyKey: r.Header.Get("Idempotency-Key")}); err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	c.credentials.ClearAuthFlowCookie(w)
	httputil.WriteNoContent(w, r)
}

func (c *PasswordResetController) preAuth(w http.ResponseWriter, r *http.Request) (httpauth.PreAuth, string, bool) {
	credential, err := c.credentials.PreAuth(r)
	if err != nil {
		httputil.WriteCredentialError(w, r, err)
		return httpauth.PreAuth{}, "", false
	}
	if credential.IntentID == "" {
		httputil.WriteError(w, r, domain.Problem(404, "AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다."))
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
