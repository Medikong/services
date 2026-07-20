package intent

import (
	"net/http"
	"strings"
	"time"

	applicationintent "github.com/Medikong/services/services/auth-service/internal/application/intent"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
)

type BootstrapController struct {
	credentials *httpauth.Credentials
	service     *applicationintent.BootstrapService
}

func NewBootstrap(credentials *httpauth.Credentials, service *applicationintent.BootstrapService) *BootstrapController {
	return &BootstrapController{credentials: credentials, service: service}
}

type createIntentRequest struct {
	ReturnPath    string         `json:"returnPath"`
	IntentType    string         `json:"intentType"`
	ActionContext map[string]any `json:"actionContext,omitempty"`
}

func (c *BootstrapController) CreateIntent(w http.ResponseWriter, r *http.Request) {
	var request createIntentRequest
	if problem := httputil.DecodeJSON(w, r, &request); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	result, err := c.service.Create(r.Context(), applicationintent.CreateInput{
		Channel:        r.Header.Get("X-Client-Channel"),
		ReturnPath:     request.ReturnPath,
		IntentType:     request.IntentType,
		ActionContext:  request.ActionContext,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	if result.Channel == "web" {
		seconds := int(time.Until(result.ExpiresAt).Seconds())
		c.credentials.SetAuthFlowCookie(w, httpauth.EncodeAuthFlow(result.IntentID, result.OwnerProof), seconds)
		httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{
			"authIntentId": result.IntentID,
			"expiresAt":    result.ExpiresAt,
			"nextPath":     "/auth/signin",
			"csrfToken":    result.CSRFToken,
		})
		return
	}
	httputil.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"authIntentId":  result.IntentID,
		"expiresAt":     result.ExpiresAt,
		"nextPath":      "/auth/signin",
		"authFlowToken": httpauth.EncodeAuthFlow(result.IntentID, result.OwnerProof),
	})
}

func (c *BootstrapController) GetMethods(w http.ResponseWriter, r *http.Request) {
	credential, err := c.credentials.PreAuth(r)
	if err != nil {
		httputil.WriteCredentialError(w, r, err)
		return
	}
	intentID := strings.TrimSpace(r.URL.Query().Get("intentId"))
	channel, appErr := c.service.GetMethods(r.Context(), intentID, credential.Token)
	if appErr != nil {
		httputil.WriteError(w, r, appErr)
		return
	}
	if channel == "" {
		channel = string(credential.Channel)
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"intentId": intentID,
		"methods": []map[string]any{
			{"type": "email_password", "enabled": true},
			{"type": "phone_otp", "enabled": true},
		},
	})
}
