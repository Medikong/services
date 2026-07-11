package controller

import (
	"net/http"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/signin"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
	"github.com/go-chi/chi/v5"
)

type SignInController struct {
	contract *httpcontract.Contract
	email    *signin.EmailService
	phone    *signin.PhoneService
}

func NewSignIn(contract *httpcontract.Contract, email *signin.EmailService, phone *signin.PhoneService) *SignInController {
	return &SignInController{contract: contract, email: email, phone: phone}
}

type phoneSignInIssueRequest struct {
	AuthIntentID string       `json:"authIntentId"`
	Phone        phoneRequest `json:"phone"`
	RememberMe   bool         `json:"rememberMe"`
}

func (c *SignInController) PhoneIssue(w http.ResponseWriter, r *http.Request) {
	var request phoneSignInIssueRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	result, err := c.phone.Issue(r.Context(), signin.PhoneIssueInput{IntentID: request.AuthIntentID, OwnerProof: credential.Token, CSRFToken: csrf, Phone: request.Phone.CountryCode + request.Phone.NationalNumber, RememberMe: request.RememberMe, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusAccepted, map[string]any{"status": "accepted", "challengeId": result.ChallengeID, "expiresAt": result.ExpiresAt})
}

type phoneSignInVerifyRequest struct {
	Code string `json:"code"`
}

func (c *SignInController) PhoneVerify(w http.ResponseWriter, r *http.Request) {
	var request phoneSignInVerifyRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, csrf, ok := c.preAuth(w, r)
	if !ok {
		return
	}
	issued, err := c.phone.Verify(r.Context(), signin.PhoneVerifyInput{IntentID: credential.IntentID, ChallengeID: chi.URLParam(r, "challengeId"), OwnerProof: credential.Token, CSRFToken: csrf, Code: request.Code, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	c.writeIssued(w, r, issued, false)
}

func (c *SignInController) preAuth(w http.ResponseWriter, r *http.Request) (httpcontract.PreAuthCredential, string, bool) {
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

type emailSignInRequest struct {
	AuthIntentID string `json:"authIntentId"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	RememberMe   bool   `json:"rememberMe"`
}

func (c *SignInController) Email(w http.ResponseWriter, r *http.Request) {
	var request emailSignInRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, credentialErr := c.contract.PreAuthCredential(r)
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
	issued, err := c.email.SignIn(r.Context(), signin.EmailInput{
		IntentID:       request.AuthIntentID,
		OwnerProof:     credential.Token,
		CSRFToken:      csrf,
		Email:          request.Email,
		Password:       request.Password,
		RememberMe:     request.RememberMe,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	c.writeIssued(w, r, issued, request.RememberMe)
}

func (c *SignInController) writeIssued(w http.ResponseWriter, r *http.Request, issued signin.Completed, remember bool) {
	if issued.WebCookie != "" {
		maxAge := 0
		if remember {
			maxAge = int(time.Until(issued.ExpiresAt).Seconds())
		}
		c.contract.IssueSessionCookie(w, issued.WebCookie, maxAge)
		c.contract.ClearAuthFlowCookie(w)
		httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
			"userId":             issued.UserID,
			"sessionId":          issued.SessionID,
			"csrfToken":          issued.CSRFToken,
			"credentialDelivery": "web_session",
			"next":               map[string]any{"path": issued.NextPath, "intentId": issued.IntentID},
		})
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
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
