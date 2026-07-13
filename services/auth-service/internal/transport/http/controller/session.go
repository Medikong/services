package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/application"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
)

type SessionController struct {
	contract httpcontract.Contract
	service  *appsession.Service
}

func NewSession(contract httpcontract.Contract, service *appsession.Service) *SessionController {
	return &SessionController{contract: contract, service: service}
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func (c *SessionController) Refresh(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if problem := httpcontract.DecodeJSON(w, r, &request); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	result, err := c.service.Refresh(r.Context(), request.RefreshToken, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{
		"sessionId":             result.SessionID,
		"accessToken":           result.AccessToken,
		"accessTokenExpiresAt":  result.AccessTokenExpiresAt,
		"refreshToken":          result.RefreshToken,
		"refreshTokenExpiresAt": result.RefreshTokenExpiresAt,
	})
}

func (c *SessionController) Logout(w http.ResponseWriter, r *http.Request) {
	if problem := decodeOptionalLogoutRequest(w, r); problem != nil {
		httpcontract.WriteProblem(w, r, problem)
		return
	}
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil && credentialErr.Kind == httpcontract.CredentialMissing {
		refresh, refreshErr := httpcontract.RefreshToken(r)
		if refreshErr != nil {
			writeCredentialError(w, r, refreshErr)
			return
		}
		if err := c.service.LogoutByRefresh(r.Context(), refresh, r.Header.Get("Idempotency-Key")); err != nil {
			writeApplicationError(w, r, err)
			return
		}
		httpcontract.WriteNoContent(w, r)
		return
	}
	if credentialErr != nil {
		writeCredentialError(w, r, credentialErr)
		return
	}
	if credential.Channel == httpcontract.CredentialChannelMobile {
		writeCredentialError(w, r, &httpcontract.CredentialError{Kind: httpcontract.CredentialMalformed})
		return
	}
	if credential.Channel == httpcontract.CredentialChannelWeb {
		csrf, problem := c.contract.WebCSRFToken(r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		if err := c.service.LogoutByWeb(r.Context(), credential.Token, csrf, r.Header.Get("Idempotency-Key")); err != nil {
			writeApplicationError(w, r, err)
			return
		}
		c.contract.ClearSessionCookie(w)
		httpcontract.WriteNoContent(w, r)
		return
	}
	writeCredentialError(w, r, &httpcontract.CredentialError{Kind: httpcontract.CredentialMalformed})
}

// logoutRequest enforces the optional OpenAPI EmptyRequest contract. A body may
// be omitted, but a supplied JSON value must be exactly an empty object.
type logoutRequest struct{}

func (*logoutRequest) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if object == nil {
		return errors.New("logout request body must be an object")
	}
	for field := range object {
		return fmt.Errorf("json: unknown field %q", field)
	}
	return nil
}

func decodeOptionalLogoutRequest(w http.ResponseWriter, r *http.Request) *httpcontract.ContractError {
	if r == nil || r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil
	}
	var request logoutRequest
	return httpcontract.DecodeJSON(w, r, &request)
}

func (c *SessionController) Context(w http.ResponseWriter, r *http.Request) {
	httpcontract.VaryCredentials(w)
	credential, credentialErr := c.contract.SessionCredential(r)
	if credentialErr != nil && credentialErr.Kind != httpcontract.CredentialMissing {
		writeCredentialError(w, r, credentialErr)
		return
	}
	web, bearer := "", ""
	if credentialErr == nil {
		web, bearer = tokenForWeb(credential), tokenForMobile(credential)
	}
	principal, err := c.service.Authenticate(r.Context(), web, bearer)
	if err != nil {
		if application.AsError(err).Status == http.StatusUnauthorized {
			httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"authenticated": false})
			return
		}
		writeApplicationError(w, r, err)
		return
	}
	if !principal.Authenticated {
		httpcontract.WriteJSON(w, r, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	data := map[string]any{"authenticated": true, "userId": principal.UserID.String(), "roles": principal.Roles, "session": map[string]any{"sessionId": principal.SessionID.String(), "channel": principal.Channel, "authenticationMethod": principal.Method, "authenticatedAt": principal.AuthenticatedAt, "expiresAt": principal.ExpiresAt}, "linkedMethodTypes": []string{}}
	if credentialErr == nil && credential.Channel == httpcontract.CredentialChannelWeb {
		csrf, csrfErr := c.service.WebCSRF(r.Context(), credential.Token)
		if csrfErr != nil {
			writeApplicationError(w, r, csrfErr)
			return
		}
		data["csrfToken"] = csrf
	}
	httpcontract.WriteJSON(w, r, http.StatusOK, data)
}

func tokenForWeb(credential httpcontract.SessionCredential) string {
	if credential.Channel == httpcontract.CredentialChannelWeb {
		return credential.Token
	}
	return ""
}

func tokenForMobile(credential httpcontract.SessionCredential) string {
	if credential.Channel == httpcontract.CredentialChannelMobile {
		return credential.Token
	}
	return ""
}
