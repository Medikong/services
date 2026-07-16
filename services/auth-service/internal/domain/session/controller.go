package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	httpcredential "github.com/Medikong/services/services/auth-service/internal/transport/credential"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
)

type SessionController struct {
	credentials *httpcredential.Credentials
	csrf        *httputil.CSRF
	service     *Service
}

func NewSession(credentials *httpcredential.Credentials, csrf *httputil.CSRF, service *Service) *SessionController {
	return &SessionController{credentials: credentials, csrf: csrf, service: service}
}

func (c *SessionController) Refresh(w http.ResponseWriter, r *http.Request) {
	credential, credentialErr := c.credentials.Session(r)
	refreshToken, csrfToken := "", ""
	if credentialErr == nil && credential.Channel == httpcredential.Web {
		refreshToken = credential.Token
		var problem error
		csrfToken, problem = c.csrf.Token(r)
		if problem != nil {
			httputil.WriteError(w, r, problem)
			return
		}
	} else if credentialErr != nil && credentialErr.Kind == httpcredential.Missing {
		var refreshErr *httpcredential.Error
		refreshToken, refreshErr = httpcredential.RefreshToken(r)
		if refreshErr != nil {
			httputil.WriteCredentialError(w, r, refreshErr)
			return
		}
	} else {
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	result, err := c.service.Refresh(r.Context(), refreshToken, csrfToken, r.Header.Get("Idempotency-Key"))
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	if result.Channel == "web" {
		c.credentials.SetSessionCookie(w, result.RefreshToken, int(time.Until(result.RefreshTokenExpiresAt).Seconds()))
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
			"credentialDelivery": "web_jwt_refresh_cookie",
			"session":            map[string]any{"sessionId": result.SessionID, "expiresAt": result.SessionExpiresAt},
			"access":             map[string]any{"accessToken": result.AccessToken, "accessTokenExpiresAt": result.AccessTokenExpiresAt},
		})
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
		"credentialDelivery": "mobile_tokens",
		"session":            map[string]any{"sessionId": result.SessionID, "expiresAt": result.SessionExpiresAt},
		"tokens": map[string]any{
			"accessToken": result.AccessToken, "accessTokenExpiresAt": result.AccessTokenExpiresAt,
			"refreshToken": result.RefreshToken, "refreshTokenExpiresAt": result.RefreshTokenExpiresAt,
		},
	})
}

func (c *SessionController) Logout(w http.ResponseWriter, r *http.Request) {
	if problem := decodeOptionalLogoutRequest(w, r); problem != nil {
		httputil.WriteError(w, r, problem)
		return
	}
	credential, credentialErr := c.credentials.Session(r)
	if credentialErr != nil && credentialErr.Kind == httpcredential.Missing {
		refresh, refreshErr := httpcredential.RefreshToken(r)
		if refreshErr != nil {
			httputil.WriteCredentialError(w, r, refreshErr)
			return
		}
		if err := c.service.LogoutByRefresh(r.Context(), refresh, r.Header.Get("Idempotency-Key")); err != nil {
			httputil.WriteError(w, r, err)
			return
		}
		httputil.WriteNoContent(w, r)
		return
	}
	if credentialErr != nil {
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	if credential.Channel == httpcredential.Mobile {
		httputil.WriteCredentialError(w, r, &httpcredential.Error{Kind: httpcredential.Malformed})
		return
	}
	if credential.Channel == httpcredential.Web {
		csrf, problem := c.csrf.Token(r)
		if problem != nil {
			httputil.WriteError(w, r, problem)
			return
		}
		if err := c.service.LogoutByWeb(r.Context(), credential.Token, csrf, r.Header.Get("Idempotency-Key")); err != nil {
			httputil.WriteError(w, r, err)
			return
		}
		c.credentials.ClearSessionCookie(w)
		httputil.WriteNoContent(w, r)
		return
	}
	httputil.WriteCredentialError(w, r, &httpcredential.Error{Kind: httpcredential.Malformed})
}

// logoutRequest allows an omitted body, but a supplied JSON value must be an
// empty object.
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

func decodeOptionalLogoutRequest(w http.ResponseWriter, r *http.Request) error {
	if r == nil || r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil
	}
	var request logoutRequest
	return httputil.DecodeJSON(w, r, &request)
}

func (c *SessionController) Context(w http.ResponseWriter, r *http.Request) {
	httputil.VaryCredentials(w)
	credential, credentialErr := c.credentials.Session(r)
	if credentialErr != nil || credential.Channel != httpcredential.Mobile {
		httputil.WriteCredentialError(w, r, credentialErr)
		return
	}
	principal, err := c.service.Authenticate(r.Context(), "", credential.Token)
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	if !principal.Authenticated {
		httputil.WriteError(w, r, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다."))
		return
	}
	data := map[string]any{"authenticated": true, "userId": principal.UserID.String(), "session": map[string]any{"sessionId": principal.SessionID.String(), "channel": principal.Channel, "authenticationMethod": principal.Method, "authenticatedAt": principal.AuthenticatedAt, "expiresAt": principal.ExpiresAt}, "linkedMethodTypes": []string{}}
	httputil.WriteJSON(w, r, http.StatusOK, data)
}
