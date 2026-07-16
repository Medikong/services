package httpauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

const (
	authFlowTokenHeader           = "X-Auth-Flow-Token"
	registrationStatusTokenHeader = "X-Registration-Status-Token"
	refreshTokenHeader            = "X-Refresh-Token"
	developmentAccessTokenHeader  = "X-Dev-Access-Token"
	defaultSessionCookieName      = "__Host-dm_refresh"
	defaultAuthFlowCookieName     = "__Host-dm_auth"
	authFlowEnvelopePrefix        = "af1"
)

type Credentials struct {
	sessionCookieName  string
	authFlowCookieName string
	cookieSecure       bool
	developmentEnabled bool
	developmentRoute   bool
	developmentToken   string
}

func New(authConfig config.AuthConfig, development config.DevelopmentConfig) *Credentials {
	sessionCookieName := strings.TrimSpace(authConfig.SessionCookieName)
	if sessionCookieName == "" {
		sessionCookieName = defaultSessionCookieName
	}
	authFlowCookieName := strings.TrimSpace(authConfig.AuthFlowCookieName)
	if authFlowCookieName == "" {
		authFlowCookieName = defaultAuthFlowCookieName
	}
	return &Credentials{
		sessionCookieName:  sessionCookieName,
		authFlowCookieName: authFlowCookieName,
		cookieSecure:       authConfig.CookieSecure,
		developmentEnabled: development.Enabled,
		developmentRoute:   development.RouteEnabled,
		developmentToken:   development.AccessToken,
	}
}

func (c *Credentials) SetSessionCookie(w http.ResponseWriter, value string, maxAgeSeconds int) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.sessionCookieName,
		Value:    value,
		Path:     "/api/v1/auth/sessions",
		HttpOnly: true,
		Secure:   c.cookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAgeSeconds,
	})
}

func (c *Credentials) SetAuthFlowCookie(w http.ResponseWriter, value string, maxAgeSeconds int) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.authFlowCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAgeSeconds,
	})
}

func (c *Credentials) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.sessionCookieName, Value: "", Path: "/api/v1/auth/sessions",
		HttpOnly: true, Secure: c.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

func (c *Credentials) ClearAuthFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.authFlowCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: c.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

type ErrorKind string

const (
	Missing   ErrorKind = "missing"
	Malformed ErrorKind = "malformed"
	Multiple  ErrorKind = "multiple"
	Rejected  ErrorKind = "rejected"
)

type Error struct {
	Kind ErrorKind
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Kind)
}

type Channel string

const (
	Web    Channel = "web"
	Mobile Channel = "mobile"
)

type PreAuth struct {
	Channel  Channel
	Token    string
	IntentID string
}

func EncodeAuthFlow(intentID, ownerProof string) string {
	if _, err := uuid.Parse(strings.TrimSpace(intentID)); err != nil || strings.TrimSpace(ownerProof) == "" {
		return ""
	}
	return authFlowEnvelopePrefix + "." + intentID + "." + ownerProof
}

type Session struct {
	Channel Channel
	Token   string
}

func (c *Credentials) PreAuth(r *http.Request) (PreAuth, *Error) {
	webToken, webPresent, err := uniqueCookie(r, c.authFlowCookieName)
	if err != nil {
		return PreAuth{}, err
	}
	mobileToken, mobilePresent, err := uniqueHeader(r, authFlowTokenHeader)
	if err != nil {
		return PreAuth{}, err
	}
	switch {
	case webPresent && mobilePresent:
		return PreAuth{}, &Error{Kind: Multiple}
	case webPresent:
		value, err := decodeAuthFlow(webToken)
		if err != nil {
			return PreAuth{}, err
		}
		value.Channel = Web
		return value, nil
	case mobilePresent:
		value, err := decodeAuthFlow(mobileToken)
		if err != nil {
			return PreAuth{}, err
		}
		value.Channel = Mobile
		return value, nil
	default:
		return PreAuth{}, &Error{Kind: Missing}
	}
}

func decodeAuthFlow(raw string) (PreAuth, *Error) {
	raw = strings.TrimSpace(raw)
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) == 3 && parts[0] == authFlowEnvelopePrefix {
		if _, err := uuid.Parse(parts[1]); err != nil || strings.TrimSpace(parts[2]) == "" {
			return PreAuth{}, &Error{Kind: Malformed}
		}
		return PreAuth{IntentID: parts[1], Token: parts[2]}, nil
	}
	if raw == "" {
		return PreAuth{}, &Error{Kind: Malformed}
	}
	return PreAuth{Token: raw}, nil
}

func (c *Credentials) Session(r *http.Request) (Session, *Error) {
	webToken, webPresent, err := uniqueCookie(r, c.sessionCookieName)
	if err != nil {
		return Session{}, err
	}
	mobileToken, mobilePresent, err := bearerToken(r)
	if err != nil {
		return Session{}, err
	}
	switch {
	case webPresent && mobilePresent:
		return Session{}, &Error{Kind: Multiple}
	case webPresent:
		return Session{Channel: Web, Token: webToken}, nil
	case mobilePresent:
		return Session{Channel: Mobile, Token: mobileToken}, nil
	default:
		return Session{}, &Error{Kind: Missing}
	}
}

func RegistrationStatusToken(r *http.Request) (string, *Error) {
	token, present, err := uniqueHeader(r, registrationStatusTokenHeader)
	if err != nil {
		return "", err
	}
	if !present {
		return "", &Error{Kind: Missing}
	}
	return token, nil
}

func RefreshToken(r *http.Request) (string, *Error) {
	token, present, err := uniqueHeader(r, refreshTokenHeader)
	if err != nil {
		return "", err
	}
	if !present {
		return "", &Error{Kind: Missing}
	}
	return token, nil
}

func (c *Credentials) DevelopmentToken(r *http.Request) *Error {
	if !c.developmentEnabled || !c.developmentRoute || c.developmentToken == "" {
		return &Error{Kind: Rejected}
	}
	token, present, err := uniqueHeader(r, developmentAccessTokenHeader)
	if err != nil {
		return err
	}
	if !present || subtle.ConstantTimeCompare([]byte(token), []byte(c.developmentToken)) != 1 {
		return &Error{Kind: Rejected}
	}
	return nil
}

func CookieMaxAge(rememberMe bool, expiresAt time.Time) int {
	if !rememberMe {
		return 0
	}
	return int(time.Until(expiresAt).Seconds())
}

func uniqueCookie(r *http.Request, name string) (string, bool, *Error) {
	if r == nil {
		return "", false, &Error{Kind: Missing}
	}
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			count++
			value = cookie.Value
		}
	}
	if count == 0 {
		return "", false, nil
	}
	if count != 1 || strings.TrimSpace(value) == "" {
		return "", false, &Error{Kind: Malformed}
	}
	return value, true, nil
}

func uniqueHeader(r *http.Request, name string) (string, bool, *Error) {
	if r == nil {
		return "", false, &Error{Kind: Missing}
	}
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false, &Error{Kind: Malformed}
	}
	return values[0], true, nil
}

func bearerToken(r *http.Request) (string, bool, *Error) {
	value, present, err := uniqueHeader(r, "Authorization")
	if err != nil || !present {
		return "", present, err
	}
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false, &Error{Kind: Malformed}
	}
	return parts[1], true, nil
}
