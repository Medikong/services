package controller

import (
	"net/http"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/bootstrap"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
)

type BootstrapController struct {
	contract httpcontract.Contract
	service  *bootstrap.Service
}

func NewBootstrap(contract httpcontract.Contract, service *bootstrap.Service) *BootstrapController {
	return &BootstrapController{contract: contract, service: service}
}

type createIntentRequest struct {
	ReturnPath    string         `json:"returnPath"`
	IntentType    string         `json:"intentType"`
	ActionContext map[string]any `json:"actionContext,omitempty"`
}

func (c *BootstrapController) CreateIntent(w http.ResponseWriter, r *http.Request) {
	var request createIntentRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	result, err := c.service.Create(r.Context(), bootstrap.CreateInput{
		Channel:        r.Header.Get("X-Client-Channel"),
		ReturnPath:     request.ReturnPath,
		IntentType:     request.IntentType,
		ActionContext:  request.ActionContext,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	if result.Channel == "web" {
		seconds := int(time.Until(result.ExpiresAt).Seconds())
		c.contract.IssueAuthFlowCookie(w, httpcontract.EncodeAuthFlowCredential(result.IntentID, result.OwnerProof), seconds)
		httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{
			"authIntentId": result.IntentID,
			"expiresAt":    result.ExpiresAt,
			"nextPath":     "/auth/signin",
			"csrfToken":    result.CSRFToken,
		})
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"authIntentId":  result.IntentID,
		"expiresAt":     result.ExpiresAt,
		"nextPath":      "/auth/signin",
		"authFlowToken": httpcontract.EncodeAuthFlowCredential(result.IntentID, result.OwnerProof),
	})
}

func (c *BootstrapController) GetMethods(w http.ResponseWriter, r *http.Request) {
	credential, err := c.contract.PreAuthCredential(r)
	if err != nil {
		writeCredentialError(w, r, err)
		return
	}
	intentID := strings.TrimSpace(r.URL.Query().Get("intentId"))
	channel, appErr := c.service.GetMethods(r.Context(), intentID, credential.Token)
	if appErr != nil {
		writeApplicationError(w, r, appErr)
		return
	}
	if channel == "" {
		channel = string(credential.Channel)
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"intentId": intentID,
		"methods": []map[string]any{
			{"type": "email_password", "enabled": true},
			{"type": "phone_otp", "enabled": true},
		},
	})
}

func writeCredentialError(w http.ResponseWriter, r *http.Request, err *httpcontract.CredentialError) {
	status := http.StatusUnauthorized
	code := "AUTH_SESSION_REQUIRED"
	if err != nil && err.Kind == httpcontract.CredentialMultiple {
		status, code = http.StatusBadRequest, "AUTH_MULTIPLE_CREDENTIALS"
	}
	httpcontract.WriteProblem(w, r, httpcontract.NewContractError(
		status, code, "요청을 처리할 수 없습니다.", "인증 정보를 확인한 뒤 다시 시도해주세요.", false,
	))
}
