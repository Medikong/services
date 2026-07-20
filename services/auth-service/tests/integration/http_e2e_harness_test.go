//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/app"
	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/cryptography"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

const (
	httpE2ETestOrigin       = "https://app.example.test"
	httpE2ECredentialKey    = "01234567890123456789012345678901"
	httpE2EReplayKey        = "01234567890123456789012345678901"
	httpE2EJWTIssuer        = "auth-http-e2e"
	httpE2EDevelopmentToken = "auth-http-e2e-development-access"
	httpE2EVirtualKey       = "01234567890123456789012345678901"
	httpE2EMaxResponseBytes = 1 << 20
)

type httpE2EHarness struct {
	t         *testing.T
	ctx       context.Context
	cancel    context.CancelFunc
	db        *pgxpool.Pool
	client    *http.Client
	baseURL   string
	adminURL  string
	origin    string
	result    <-chan error
	closeOnce sync.Once
}

type httpE2ERequest struct {
	Method        string
	Path          string
	JSON          any
	RawJSON       []byte
	ContentType   string
	Headers       http.Header
	Credentials   httpE2ECredentials
	RequestID     string
	OmitRequestID bool
}

type httpE2ECredentials struct {
	AuthFlowCookie          *http.Cookie
	SessionCookie           *http.Cookie
	AuthFlowToken           string
	RegistrationStatusToken string
	AccessToken             string
	RefreshToken            string
	CSRFToken               string
	DevelopmentAccessToken  string
	Origin                  string
}

type httpE2EResponse struct {
	status            int
	header            http.Header
	cookies           []*http.Cookie
	body              []byte
	inboundRequestID  string
	requestIDWasValid bool
}

type httpE2EResponseMeta struct {
	RequestID string `json:"requestId"`
}

func newProductionHTTPHarness(t *testing.T, options ...app.ServerOptions) *httpE2EHarness {
	t.Helper()
	return newHTTPHarness(t, false, serverOptions(t, options))
}

func newDevelopmentHTTPHarness(t *testing.T, options ...app.ServerOptions) *httpE2EHarness {
	t.Helper()
	return newHTTPHarness(t, true, serverOptions(t, options))
}

func serverOptions(t *testing.T, options []app.ServerOptions) app.ServerOptions {
	t.Helper()
	if len(options) > 1 {
		t.Fatal("HTTP E2E server accepts at most one option set")
	}
	if len(options) == 1 {
		return options[0]
	}
	return app.ServerOptions{}
}

func newHTTPHarness(t *testing.T, development bool, options app.ServerOptions) *httpE2EHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	databaseURL := startPostgres(t, ctx)
	var cfg config.ServerConfig
	if development {
		cfg = loadDevelopmentHTTPServerConfig(t, databaseURL)
	} else {
		cfg = loadProductionHTTPServerConfig(t, databaseURL)
	}
	var db *pgxpool.Pool
	if development {
		db = migrateSchemas(t, ctx, cfg.Postgres)
	} else {
		db = migrateProductionSchemas(t, ctx, cfg.Postgres)
	}
	t.Cleanup(db.Close)

	runCtx, stop := context.WithCancel(ctx)
	server, err := app.NewServer(ctx, cfg, options)
	if err != nil {
		stop()
		t.Fatal("construct HTTP E2E server")
	}
	result := make(chan error, 1)
	go func() { result <- server.Run(runCtx) }()

	harness := &httpE2EHarness{
		t:      t,
		ctx:    ctx,
		cancel: stop,
		db:     db,
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL:  "http://" + cfg.HTTP.PublicAddr,
		adminURL: "http://" + cfg.HTTP.AdminAddr,
		origin:   httpE2ETestOrigin,
		result:   result,
	}
	t.Cleanup(harness.close)
	waitReady(t, harness.adminURL+"/readyz")
	return harness
}

func loadProductionHTTPServerConfig(t *testing.T, databaseURL string) config.ServerConfig {
	t.Helper()
	configureHTTPEnvironment(t, databaseURL)
	t.Setenv("SERVICE_ENVIRONMENT", "production")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "")
	t.Setenv("AUTH_DEV_ROUTE_ENABLED", "")
	t.Setenv("AUTH_VIRTUAL_ADAPTERS_ENABLED", "")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", "")
	t.Setenv("AUTH_VIRTUAL_MESSAGE_KEY", "")
	return loadHTTPServerConfig(t)
}

func loadDevelopmentHTTPServerConfig(t *testing.T, databaseURL string) config.ServerConfig {
	t.Helper()
	configureHTTPEnvironment(t, databaseURL)
	t.Setenv("SERVICE_ENVIRONMENT", "test")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "true")
	t.Setenv("AUTH_DEV_ROUTE_ENABLED", "true")
	t.Setenv("AUTH_VIRTUAL_ADAPTERS_ENABLED", "true")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", httpE2EDevelopmentToken)
	t.Setenv("AUTH_VIRTUAL_MESSAGE_KEY", httpE2EVirtualKey)
	return loadHTTPServerConfig(t)
}

func configureHTTPEnvironment(t *testing.T, databaseURL string) {
	t.Helper()
	t.Setenv("SERVICE_NAME", config.ServiceName)
	t.Setenv("SERVICE_VERSION", "http-e2e")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("AUTH_CREDENTIAL_HMAC_KEY", httpE2ECredentialKey)
	t.Setenv("AUTH_REPLAY_ENCRYPTION_KEY", httpE2EReplayKey)
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", integrationJWTPrivateKeyPEM(t))
	t.Setenv("AUTH_JWT_KEY_ID", "http-e2e-key")
	t.Setenv("AUTH_JWT_ISSUER", httpE2EJWTIssuer)
	t.Setenv("AUTH_JWT_AUDIENCES", "dropmong-api")
	t.Setenv("AUTH_PROOF_PRIVATE_KEY", integrationAuthProofPrivateKey())
	t.Setenv("AUTH_PROOF_KEY_ID", "auth-local-1")
	t.Setenv("AUTH_USER_PROOF_PUBLIC_KEY", integrationUserProofPublicKey())
	t.Setenv("AUTH_USER_PROOF_KEY_ID", "user-local-1")
	t.Setenv("AUTH_USER_PROOF_ISSUER", "user-service")
	t.Setenv("AUTH_ALLOWED_ORIGINS", httpE2ETestOrigin)
	t.Setenv("AUTH_COOKIE_SECURE", "true")
	t.Setenv("AUTH_SESSION_COOKIE_NAME", "__Secure-dm_refresh")
	t.Setenv("AUTH_FLOW_COOKIE_NAME", "__Host-dm_auth")
	t.Setenv("AUTH_INTENT_TTL", "15m")
	t.Setenv("AUTH_REGISTRATION_TTL", "30m")
	t.Setenv("AUTH_CHALLENGE_TTL", "10m")
	t.Setenv("AUTH_SESSION_TTL", "1h")
	t.Setenv("AUTH_REMEMBER_ME_SESSION_TTL", "24h")
	t.Setenv("AUTH_REFRESH_TTL", "1h")
	t.Setenv("AUTH_ACCESS_TTL", "5m")
	t.Setenv("AUTH_PROOF_TTL", "5m")
	t.Setenv("AUTH_RECOVERY_TTL", "2m")
	t.Setenv("AUTH_PASSWORD_MIN_LENGTH", "12")
	t.Setenv("READINESS_CHECK_TIMEOUT", "1s")
	t.Setenv("SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("DRAIN_DELAY", "1ms")
	t.Setenv("PPROF_ENABLED", "false")
	t.Setenv("PYROSCOPE_ENABLED", "false")
}

func loadHTTPServerConfig(t *testing.T) config.ServerConfig {
	t.Helper()
	cfg, err := config.LoadServer()
	if err != nil {
		t.Fatal("load HTTP E2E server config")
	}
	cfg.HTTP.PublicAddr = unusedAddress(t)
	cfg.HTTP.AdminAddr = unusedAddress(t)
	cfg.HTTP.DrainDelay = time.Millisecond
	cfg.Lifecycle.ShutdownTimeout = 2 * time.Second
	return cfg
}

func (h *httpE2EHarness) close() {
	h.closeOnce.Do(func() {
		h.cancel()
		assertStopped(h.t, h.result, "HTTP E2E server")
	})
}

func (h *httpE2EHarness) do(request httpE2ERequest) *httpE2EResponse {
	h.t.Helper()
	if request.Method == "" || !strings.HasPrefix(request.Path, "/") {
		h.t.Fatal("HTTP E2E request requires a method and an absolute path")
	}
	if request.JSON != nil && request.RawJSON != nil {
		h.t.Fatal("HTTP E2E request cannot use JSON and RawJSON together")
	}

	var body io.Reader
	if request.JSON != nil {
		encoded, err := json.Marshal(request.JSON)
		if err != nil {
			h.t.Fatal("encode HTTP E2E request")
		}
		body = bytes.NewReader(encoded)
	} else if request.RawJSON != nil {
		body = bytes.NewReader(request.RawJSON)
	}
	httpRequest, err := http.NewRequestWithContext(h.ctx, request.Method, h.baseURL+request.Path, body)
	if err != nil {
		h.t.Fatal("create HTTP E2E request")
	}
	for name, values := range request.Headers {
		for _, value := range values {
			httpRequest.Header.Add(name, value)
		}
	}
	if body != nil {
		contentType := request.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		httpRequest.Header.Set("Content-Type", contentType)
	}
	request.Credentials.apply(h.t, httpRequest)

	inboundRequestID := request.RequestID
	requestIDWasValid := false
	if !request.OmitRequestID {
		if inboundRequestID == "" {
			inboundRequestID = uuid.NewString()
		}
		httpRequest.Header.Set("X-Request-Id", inboundRequestID)
		if parsed, err := uuid.Parse(strings.TrimSpace(inboundRequestID)); err == nil {
			requestIDWasValid = true
			inboundRequestID = parsed.String()
		}
	}

	response, err := h.client.Do(httpRequest)
	if err != nil {
		h.t.Fatal("execute HTTP E2E request")
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, httpE2EMaxResponseBytes+1))
	if err != nil {
		h.t.Fatal("read HTTP E2E response")
	}
	if len(responseBody) > httpE2EMaxResponseBytes {
		h.t.Fatal("HTTP E2E response exceeds the safe size limit")
	}
	return &httpE2EResponse{
		status:            response.StatusCode,
		header:            response.Header.Clone(),
		cookies:           response.Cookies(),
		body:              responseBody,
		inboundRequestID:  inboundRequestID,
		requestIDWasValid: requestIDWasValid,
	}
}

func (credentials httpE2ECredentials) apply(t *testing.T, request *http.Request) {
	t.Helper()
	for _, cookie := range []*http.Cookie{credentials.AuthFlowCookie, credentials.SessionCookie} {
		if cookie == nil {
			continue
		}
		if strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			t.Fatal("HTTP E2E credential cookie is incomplete")
		}
		request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	setHeaderIfPresent(request.Header, "X-Auth-Flow-Token", credentials.AuthFlowToken)
	setHeaderIfPresent(request.Header, "X-Registration-Status-Token", credentials.RegistrationStatusToken)
	if credentials.AccessToken != "" {
		request.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	}
	setHeaderIfPresent(request.Header, "X-Refresh-Token", credentials.RefreshToken)
	setHeaderIfPresent(request.Header, "X-CSRF-Token", credentials.CSRFToken)
	setHeaderIfPresent(request.Header, "X-Dev-Access-Token", credentials.DevelopmentAccessToken)
	setHeaderIfPresent(request.Header, "Origin", credentials.Origin)
}

func setHeaderIfPresent(header http.Header, name, value string) {
	if value != "" {
		header.Set(name, value)
	}
}

func decodeHTTPEnvelope(t *testing.T, response *httpE2EResponse, wantStatus int, data any) httpE2EResponseMeta {
	t.Helper()
	assertHTTPResponse(t, response, wantStatus, "application/json")
	var envelope struct {
		Data json.RawMessage     `json:"data"`
		Meta httpE2EResponseMeta `json:"meta"`
	}
	decodeHTTPJSON(t, response.body, &envelope)
	if data == nil || len(envelope.Data) == 0 {
		t.Fatal("HTTP E2E success envelope is incomplete")
	}
	decodeHTTPJSON(t, envelope.Data, data)
	if envelope.Meta.RequestID != response.header.Get("X-Request-Id") {
		t.Fatal("HTTP E2E envelope request ID does not match the response header")
	}
	return envelope.Meta
}

func decodeHTTPError(t *testing.T, response *httpE2EResponse, wantStatus int, wantCode string) httputil.Error {
	t.Helper()
	assertHTTPResponse(t, response, wantStatus, "application/json")
	var apiError httputil.Error
	decodeHTTPJSON(t, response.body, &apiError)
	if apiError.Status != wantStatus || apiError.Code != wantCode || apiError.RequestID != response.header.Get("X-Request-Id") {
		t.Fatal("HTTP E2E error does not match the expected response")
	}
	if apiError.Message == "" {
		t.Fatal("HTTP E2E error is incomplete")
	}
	return apiError
}

func errorCode(err error) string {
	var typed *failure.Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	oopsErr, ok := oops.AsOops(err)
	if !ok {
		return ""
	}
	code, _ := oopsErr.Code().(string)
	return code
}

func assertHTTPNoContent(t *testing.T, response *httpE2EResponse, wantStatus int) {
	t.Helper()
	assertHTTPResponse(t, response, wantStatus, "")
	if len(bytes.TrimSpace(response.body)) != 0 {
		t.Fatal("HTTP E2E no-content response contains a body")
	}
}

func assertHTTPResponse(t *testing.T, response *httpE2EResponse, wantStatus int, wantMediaType string) {
	t.Helper()
	if response == nil {
		t.Fatal("HTTP E2E response is nil")
	}
	if response.status != wantStatus {
		var safeError struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.body, &safeError)
		t.Fatalf("HTTP E2E status = %d, want %d, error code = %q", response.status, wantStatus, safeError.Code)
	}
	if response.header.Get("Cache-Control") != "no-store" {
		t.Fatal("HTTP E2E response is missing Cache-Control: no-store")
	}
	responseRequestID := strings.TrimSpace(response.header.Get("X-Request-Id"))
	parsedRequestID, err := uuid.Parse(responseRequestID)
	if err != nil {
		t.Fatal("HTTP E2E response request ID is not a UUID")
	}
	if response.requestIDWasValid && parsedRequestID.String() != response.inboundRequestID {
		t.Fatal("HTTP E2E response did not preserve the valid request ID")
	}
	if wantMediaType == "" {
		return
	}
	mediaType, _, err := mime.ParseMediaType(response.header.Get("Content-Type"))
	if err != nil || mediaType != wantMediaType {
		t.Fatal("HTTP E2E response has an unexpected content type")
	}
}

func decodeHTTPJSON(t *testing.T, body []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatal("decode HTTP E2E response JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatal("HTTP E2E response JSON has trailing data")
	}
}

func responseCookie(t *testing.T, response *httpE2EResponse, name string) *http.Cookie {
	t.Helper()
	found := namedResponseCookie(t, response, name)
	if strings.TrimSpace(found.Value) == "" {
		t.Fatal("HTTP E2E response is missing the credential cookie")
	}
	return found
}

func clearedResponseCookie(t *testing.T, response *httpE2EResponse, name string) *http.Cookie {
	t.Helper()
	found := namedResponseCookie(t, response, name)
	if found.Value != "" || found.MaxAge >= 0 {
		t.Fatal("HTTP E2E response did not clear the credential cookie")
	}
	return found
}

func namedResponseCookie(t *testing.T, response *httpE2EResponse, name string) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, cookie := range response.cookies {
		if cookie.Name != name {
			continue
		}
		if found != nil {
			t.Fatal("HTTP E2E response contains a duplicate credential cookie")
		}
		copy := *cookie
		found = &copy
	}
	if found == nil {
		t.Fatal("HTTP E2E response is missing the credential cookie")
	}
	return found
}

func assertCredentialCookie(t *testing.T, cookie *http.Cookie, name string) {
	t.Helper()
	wantPath, wantSameSite := "/", http.SameSiteLaxMode
	if name == "__Secure-dm_refresh" {
		wantPath, wantSameSite = "/api/v1/auth/sessions", http.SameSiteStrictMode
	}
	if cookie == nil || cookie.Name != name || cookie.Value == "" || cookie.Path != wantPath || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != wantSameSite {
		t.Fatal("HTTP E2E credential cookie attributes are invalid")
	}
}

func assertResponseOmits(t *testing.T, response *httpE2EResponse, protectedValues ...string) {
	t.Helper()
	if response == nil {
		t.Fatal("HTTP E2E response is nil")
	}
	for _, value := range protectedValues {
		if value == "" {
			continue
		}
		if bytes.Contains(response.body, []byte(value)) {
			t.Fatal("HTTP E2E response exposed a protected fixture value")
		}
		for _, values := range response.header {
			for _, headerValue := range values {
				if strings.Contains(headerValue, value) {
					t.Fatal("HTTP E2E response header exposed a protected fixture value")
				}
			}
		}
	}
}

func assertHTTPStatus(t *testing.T, response *httpE2EResponse, wantStatus int) {
	t.Helper()
	if response == nil || response.status != wantStatus {
		t.Fatal("HTTP E2E response has an unexpected status")
	}
}

func decryptDeliveryCode(t *testing.T, ctx context.Context, db *pgxpool.Pool, challengeID string) string {
	t.Helper()
	id, err := uuid.Parse(challengeID)
	if err != nil {
		t.Fatal("HTTP E2E challenge ID is invalid")
	}
	var ciphertext []byte
	if err := db.QueryRow(ctx, `
		SELECT payload_ciphertext
		FROM auth_verification_delivery_payloads
		WHERE challenge_id = $1
		ORDER BY send_sequence DESC
		LIMIT 1
	`, id).Scan(&ciphertext); err != nil {
		t.Fatal("read encrypted HTTP E2E delivery payload")
	}
	var payload struct {
		Code        string `json:"code"`
		Destination string `json:"destination"`
	}
	keys := cryptography.Keys{ReplayKey: []byte(httpE2EReplayKey)}
	if err := keys.Open(ciphertext, &payload); err != nil {
		t.Fatal("decrypt HTTP E2E delivery payload")
	}
	if len(payload.Code) != 6 || strings.TrimSpace(payload.Destination) == "" {
		t.Fatal("HTTP E2E delivery payload is incomplete")
	}
	return payload.Code
}
