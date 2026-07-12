package httpcontract

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

const (
	requestIDHeader               = "X-Request-Id"
	cacheControlHeader            = "Cache-Control"
	cacheControlNoStore           = "no-store"
	csrfHeader                    = "X-CSRF-Token"
	authFlowTokenHeader           = "X-Auth-Flow-Token"
	registrationStatusTokenHeader = "X-Registration-Status-Token"
	refreshTokenHeader            = "X-Refresh-Token"
	developmentAccessTokenHeader  = "X-Dev-Access-Token"
	defaultSessionCookieName      = "__Host-dm_session"
	defaultAuthFlowCookieName     = "__Host-dm_auth"
	maxJSONBodyBytes              = 1 << 20
)

type requestIDContextKey struct{}

// Contract holds HTTP-only authentication settings. It deliberately contains
// no user identity data and never logs credential values.
type Contract struct {
	sessionCookieName  string
	authFlowCookieName string
	cookieSecure       bool
	allowedOrigins     map[string]struct{}
	developmentEnabled bool
	developmentRoute   bool
	developmentToken   string
}

// NewContract builds the shared transport contract from validated service
// configuration.
func NewContract(authConfig config.AuthConfig, development config.DevelopmentConfig) Contract {
	sessionCookieName := strings.TrimSpace(authConfig.SessionCookieName)
	if sessionCookieName == "" {
		sessionCookieName = defaultSessionCookieName
	}
	authFlowCookieName := strings.TrimSpace(authConfig.AuthFlowCookieName)
	if authFlowCookieName == "" {
		authFlowCookieName = defaultAuthFlowCookieName
	}
	allowedOrigins := make(map[string]struct{}, len(authConfig.AllowedOrigins))
	for _, origin := range authConfig.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowedOrigins[origin] = struct{}{}
		}
	}
	return Contract{
		sessionCookieName:  sessionCookieName,
		authFlowCookieName: authFlowCookieName,
		cookieSecure:       authConfig.CookieSecure,
		allowedOrigins:     allowedOrigins,
		developmentEnabled: development.Enabled,
		developmentRoute:   development.RouteEnabled,
		developmentToken:   development.AccessToken,
	}
}

// ResponseMeta is the required meta block for every successful JSON response.
type ResponseMeta struct {
	RequestID string `json:"requestId"`
}

// Envelope is the production success-response shape defined by OpenAPI.
type Envelope struct {
	Data any          `json:"data"`
	Meta ResponseMeta `json:"meta"`
}

// RequestIDMiddleware normalizes the inbound request ID and guarantees the
// response header even when a handler writes no JSON body.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := EnsureRequestID(r)
		w.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, requestID)))
	})
}

// EnsureRequestID accepts only a UUID and normalizes it to canonical form.
// Invalid or missing values are replaced before any response is emitted.
func EnsureRequestID(r *http.Request) string {
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	if parsed, err := uuid.Parse(strings.TrimSpace(r.Header.Get(requestIDHeader))); err == nil {
		requestID := parsed.String()
		r.Header.Set(requestIDHeader, requestID)
		return requestID
	}
	requestID := uuid.NewString()
	r.Header.Set(requestIDHeader, requestID)
	return requestID
}

func requestIDFor(r *http.Request) string {
	if r != nil {
		if requestID, ok := r.Context().Value(requestIDContextKey{}).(string); ok && requestID != "" {
			return requestID
		}
		return EnsureRequestID(r)
	}
	return uuid.NewString()
}

func setCommonResponseHeaders(w http.ResponseWriter, requestID string) {
	w.Header().Set(requestIDHeader, requestID)
	w.Header().Set(cacheControlHeader, cacheControlNoStore)
}

// WriteJSON writes the production response envelope. Authentication responses
// are always no-store, including successful responses with credentials.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	if status == http.StatusNoContent {
		WriteNoContent(w, r)
		return
	}
	requestID := requestIDFor(r)
	setCommonResponseHeaders(w, requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Data: data, Meta: ResponseMeta{RequestID: requestID}})
}

// WriteNoContent writes a no-store response for the OpenAPI 204 operations.
func WriteNoContent(w http.ResponseWriter, r *http.Request) {
	setCommonResponseHeaders(w, requestIDFor(r))
	w.WriteHeader(http.StatusNoContent)
}

// VaryCredentials marks optional-auth responses, such as auth context, as
// credential-specific for shared caches.
func VaryCredentials(w http.ResponseWriter) {
	w.Header().Set("Vary", "Cookie, Authorization")
}

// IssueSessionCookie emits one Set-Cookie field. maxAgeSeconds=0 creates a
// browser-session cookie; a positive value is for remember-me sessions.
func (c Contract) IssueSessionCookie(w http.ResponseWriter, value string, maxAgeSeconds int) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAgeSeconds,
	})
}

// IssueAuthFlowCookie emits the short-lived web pre-authentication cookie.
func (c Contract) IssueAuthFlowCookie(w http.ResponseWriter, value string, maxAgeSeconds int) {
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

func (c Contract) ClearSessionCookie(w http.ResponseWriter) {
	c.clearCookie(w, c.sessionCookieName)
}

func (c Contract) ClearAuthFlowCookie(w http.ResponseWriter) {
	c.clearCookie(w, c.authFlowCookieName)
}

func (c Contract) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   c.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// CredentialKind lets endpoint handlers map a transport failure to the
// operation-specific OpenAPI code without examining any secret value.
type CredentialKind string

const (
	CredentialMissing   CredentialKind = "missing"
	CredentialMalformed CredentialKind = "malformed"
	CredentialMultiple  CredentialKind = "multiple"
	CredentialRejected  CredentialKind = "rejected"
)

type CredentialError struct {
	Kind CredentialKind
}

func (e *CredentialError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Kind)
}

type CredentialChannel string

const (
	CredentialChannelWeb    CredentialChannel = "web"
	CredentialChannelMobile CredentialChannel = "mobile"
)

// PreAuthCredential is either the web auth-flow cookie or the mobile flow
// token; callers must not infer this from X-Client-Channel alone.
type PreAuthCredential struct {
	Channel  CredentialChannel
	Token    string // owner proof, never the wire envelope
	IntentID string
}

const authFlowEnvelopePrefix = "af1"

// EncodeAuthFlowCredential keeps the AuthenticationIntent binding inside the
// opaque web-cookie/mobile-header value. The owner proof remains random and
// opaque; callers never persist or log this envelope.
func EncodeAuthFlowCredential(intentID, ownerProof string) string {
	if _, err := uuid.Parse(strings.TrimSpace(intentID)); err != nil || strings.TrimSpace(ownerProof) == "" {
		return ""
	}
	return authFlowEnvelopePrefix + "." + intentID + "." + ownerProof
}

// SessionCredential is either the web session cookie or the mobile JWT.
type SessionCredential struct {
	Channel CredentialChannel
	Token   string
}

func (c Contract) PreAuthCredential(r *http.Request) (PreAuthCredential, *CredentialError) {
	webToken, webPresent, err := uniqueCookie(r, c.authFlowCookieName)
	if err != nil {
		return PreAuthCredential{}, err
	}
	mobileToken, mobilePresent, err := uniqueHeader(r, authFlowTokenHeader)
	if err != nil {
		return PreAuthCredential{}, err
	}
	switch {
	case webPresent && mobilePresent:
		return PreAuthCredential{}, &CredentialError{Kind: CredentialMultiple}
	case webPresent:
		credential, err := decodeAuthFlowCredential(webToken)
		if err != nil {
			return PreAuthCredential{}, err
		}
		credential.Channel = CredentialChannelWeb
		return credential, nil
	case mobilePresent:
		credential, err := decodeAuthFlowCredential(mobileToken)
		if err != nil {
			return PreAuthCredential{}, err
		}
		credential.Channel = CredentialChannelMobile
		return credential, nil
	default:
		return PreAuthCredential{}, &CredentialError{Kind: CredentialMissing}
	}
}

func decodeAuthFlowCredential(raw string) (PreAuthCredential, *CredentialError) {
	raw = strings.TrimSpace(raw)
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) == 3 && parts[0] == authFlowEnvelopePrefix {
		if _, err := uuid.Parse(parts[1]); err != nil || strings.TrimSpace(parts[2]) == "" {
			return PreAuthCredential{}, &CredentialError{Kind: CredentialMalformed}
		}
		return PreAuthCredential{IntentID: parts[1], Token: parts[2]}, nil
	}
	// Legacy opaque flow credentials remain accepted for endpoints whose body
	// contains authIntentId. New issuers always use the bound envelope.
	if raw == "" {
		return PreAuthCredential{}, &CredentialError{Kind: CredentialMalformed}
	}
	return PreAuthCredential{Token: raw}, nil
}

func (c Contract) SessionCredential(r *http.Request) (SessionCredential, *CredentialError) {
	webToken, webPresent, err := uniqueCookie(r, c.sessionCookieName)
	if err != nil {
		return SessionCredential{}, err
	}
	mobileToken, mobilePresent, err := bearerToken(r)
	if err != nil {
		return SessionCredential{}, err
	}
	switch {
	case webPresent && mobilePresent:
		return SessionCredential{}, &CredentialError{Kind: CredentialMultiple}
	case webPresent:
		return SessionCredential{Channel: CredentialChannelWeb, Token: webToken}, nil
	case mobilePresent:
		return SessionCredential{Channel: CredentialChannelMobile, Token: mobileToken}, nil
	default:
		return SessionCredential{}, &CredentialError{Kind: CredentialMissing}
	}
}

func RegistrationStatusToken(r *http.Request) (string, *CredentialError) {
	token, present, err := uniqueHeader(r, registrationStatusTokenHeader)
	if err != nil {
		return "", err
	}
	if !present {
		return "", &CredentialError{Kind: CredentialMissing}
	}
	return token, nil
}

func RefreshToken(r *http.Request) (string, *CredentialError) {
	token, present, err := uniqueHeader(r, refreshTokenHeader)
	if err != nil {
		return "", err
	}
	if !present {
		return "", &CredentialError{Kind: CredentialMissing}
	}
	return token, nil
}

// DevelopmentAccessToken verifies the development Gateway token without
// revealing whether a virtual verification message exists.
func (c Contract) DevelopmentAccessToken(r *http.Request) *CredentialError {
	if !c.developmentEnabled || !c.developmentRoute || c.developmentToken == "" {
		return &CredentialError{Kind: CredentialRejected}
	}
	token, present, err := uniqueHeader(r, developmentAccessTokenHeader)
	if err != nil {
		return err
	}
	if !present || subtle.ConstantTimeCompare([]byte(token), []byte(c.developmentToken)) != 1 {
		return &CredentialError{Kind: CredentialRejected}
	}
	return nil
}

// WebCSRFToken requires an allowlisted Origin and exactly one non-empty CSRF
// header. Binding the token to a credential is deliberately delegated to the
// authenticated domain operation.
func (c Contract) WebCSRFToken(r *http.Request) (string, *ContractError) {
	if err := c.RequireOrigin(r); err != nil {
		return "", err
	}
	tokens := r.Header.Values(csrfHeader)
	if len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "" {
		return "", csrfInvalid()
	}
	return tokens[0], nil
}

// VerifyWebCSRF completes transport and domain CSRF verification. Supplying a
// nil verifier is invalid so a handler cannot accidentally skip binding.
func (c Contract) VerifyWebCSRF(r *http.Request, verify func(string) bool) *ContractError {
	token, err := c.WebCSRFToken(r)
	if err != nil {
		return err
	}
	if verify == nil || !verify(token) {
		return csrfInvalid()
	}
	return nil
}

// RequireOrigin performs an exact allowlist comparison after rejecting any
// non-origin value such as a URL with a path, query, or fragment.
func (c Contract) RequireOrigin(r *http.Request) *ContractError {
	origin := r.Header.Get("Origin")
	parsed, err := url.Parse(origin)
	if origin == "" || err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || origin != parsed.Scheme+"://"+parsed.Host {
		return csrfInvalid()
	}
	if _, ok := c.allowedOrigins[origin]; !ok {
		return csrfInvalid()
	}
	return nil
}

// DecodeJSON enforces the request-body portions of the OpenAPI contract:
// application/json only, one JSON value, no unknown fields, and a bounded body.
func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) *ContractError {
	if target == nil || r == nil || r.Body == nil {
		return inputInvalid("missing_body")
	}
	contentType := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return inputInvalid("unsupported_media_type")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return inputInvalid("trailing_data")
		}
		return decodeError(err)
	}
	return nil
}

func decodeError(err error) *ContractError {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return inputInvalid("body_too_large")
	}
	if errors.Is(err, io.EOF) {
		return inputInvalid("missing_body")
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		return inputInvalid("invalid_type")
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return inputInvalid("additional_property")
	}
	return inputInvalid("invalid_json")
}

func uniqueCookie(r *http.Request, name string) (string, bool, *CredentialError) {
	if r == nil {
		return "", false, &CredentialError{Kind: CredentialMissing}
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
		return "", false, &CredentialError{Kind: CredentialMalformed}
	}
	return value, true, nil
}

func uniqueHeader(r *http.Request, name string) (string, bool, *CredentialError) {
	if r == nil {
		return "", false, &CredentialError{Kind: CredentialMissing}
	}
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false, &CredentialError{Kind: CredentialMalformed}
	}
	return values[0], true, nil
}

func bearerToken(r *http.Request) (string, bool, *CredentialError) {
	value, present, err := uniqueHeader(r, "Authorization")
	if err != nil || !present {
		return "", present, err
	}
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false, &CredentialError{Kind: CredentialMalformed}
	}
	return parts[1], true, nil
}
