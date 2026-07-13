package controller

import (
	"net/http"
	"strings"

	appregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
)

// RegistrationController is intentionally thin: it handles the external
// OpenAPI contract and delegates all state changes to the Registration
// application service.
type RegistrationController struct {
	contract httpcontract.Contract
	service  *appregistration.Service
}

func NewRegistration(contract httpcontract.Contract, service *appregistration.Service) *RegistrationController {
	return &RegistrationController{contract: contract, service: service}
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
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.Start(r.Context(), appregistration.StartInput{
		IntentID: request.AuthIntentID, OwnerProof: credential.Token, CSRFToken: csrf,
		Email: request.Email, Password: request.Password, Phone: request.Phone.CountryCode + request.Phone.NationalNumber,
		ProfileRequestID: request.ProfileRequestID, AgreementReceiptID: request.AgreementReceiptID,
		RememberMe: request.RememberMe, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{
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
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.IssueChallenge(r.Context(), appregistration.IssueChallengeInput{
		RegistrationID: chi.URLParam(r, "registrationId"), OwnerProof: credential.Token, CSRFToken: csrf,
		Method: request.Method, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"challengeId": result.ChallengeID, "method": result.Method, "maskedDestination": result.MaskedDestination,
		"expiresAt": result.ExpiresAt, "resendAvailableAt": result.ResendAvailableAt,
	})
}

type verifyRegistrationChallengeRequest struct {
	Code string `json:"code"`
}

func (c *RegistrationController) VerifyChallenge(w http.ResponseWriter, r *http.Request) {
	var request verifyRegistrationChallengeRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.VerifyChallenge(r.Context(), appregistration.VerifyChallengeInput{
		RegistrationID: chi.URLParam(r, "registrationId"), ChallengeID: chi.URLParam(r, "challengeId"),
		OwnerProof: credential.Token, CSRFToken: csrf, Code: request.Code, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"challengeId": result.ChallengeID, "status": result.Status,
		"registration": map[string]any{
			"status": result.RegistrationStatus, "verifiedMethods": result.VerifiedMethods,
			"requiredVerifications": []string{"email", "phone"},
		},
	})
}

type emptyRequest struct{}

func (c *RegistrationController) Complete(w http.ResponseWriter, r *http.Request) {
	var request emptyRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.service.Complete(r.Context(), appregistration.CompleteInput{
		RegistrationID: chi.URLParam(r, "registrationId"), OwnerProof: credential.Token, CSRFToken: csrf, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	if result.Pending {
		statusPath := "/api/v1/auth/registrations/" + result.RegistrationID
		w.Header().Set("Location", statusPath)
		w.Header().Set("Retry-After", "2")
		httpcontract.WriteJSON(w, r, http.StatusAccepted, map[string]any{"registrationId": result.RegistrationID, "status": result.Status, "retryable": true, "statusPath": statusPath})
		return
	}
	if result.Issued.WebCookie != "" {
		c.contract.IssueSessionCookie(w, result.Issued.WebCookie, sessionCookieMaxAge(result.Issued.RememberMe, result.Issued.ExpiresAt))
		c.contract.ClearAuthFlowCookie(w)
		httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
			"registrationId": result.RegistrationID, "status": result.Status, "credentialDelivery": "web_session",
			"userId": result.Issued.UserID, "sessionId": result.Issued.SessionID, "csrfToken": result.Issued.CSRFToken,
			"next": map[string]any{"path": result.NextPath, "intentId": result.IntentID},
		})
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registrationId": result.RegistrationID, "status": result.Status, "credentialDelivery": "mobile_tokens",
		"userId": result.Issued.UserID, "session": map[string]any{"sessionId": result.Issued.SessionID, "expiresAt": result.Issued.ExpiresAt},
		"tokens": map[string]any{"accessToken": result.Issued.AccessToken, "accessTokenExpiresAt": result.Issued.AccessTokenExpiresAt, "refreshToken": result.Issued.RefreshToken, "refreshTokenExpiresAt": result.Issued.RefreshTokenExpiresAt},
		"next":   map[string]any{"path": result.NextPath, "intentId": result.IntentID},
	})
}

func (c *RegistrationController) Status(w http.ResponseWriter, r *http.Request) {
	ownerProof, csrf := "", ""
	if credential, credentialErr := c.contract.PreAuthCredential(r); credentialErr == nil {
		ownerProof = credential.Token
		if credential.Channel == httpcontract.CredentialChannelWeb {
			// GET needs no CSRF check, but a supplied token is not required either.
			csrf = strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		}
	}
	statusToken, _ := httpcontract.RegistrationStatusToken(r)
	result, err := c.service.Status(r.Context(), appregistration.StatusInput{
		RegistrationID: chi.URLParam(r, "registrationId"), OwnerProof: ownerProof, CSRFToken: csrf, StatusToken: statusToken,
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registrationId": result.RegistrationID, "status": result.Status, "verifiedMethods": result.VerifiedMethods,
		"retryable": result.Retryable, "expiresAt": result.ExpiresAt,
	})
}

func (c *RegistrationController) preAuth(w http.ResponseWriter, r *http.Request) (httpcontract.PreAuthCredential, string, bool) {
	credential, err := c.contract.PreAuthCredential(r)
	if err != nil {
		writeCredentialError(w, r, err)
		return httpcontract.PreAuthCredential{}, "", false
	}
	if credential.Channel != httpcontract.CredentialChannelWeb {
		return credential, "", true
	}
	csrf, problem := c.contract.WebCSRFToken(r)
	if problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return httpcontract.PreAuthCredential{}, "", false
	}
	return credential, csrf, true
}
