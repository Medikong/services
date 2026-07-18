package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func Test_ExtAuthz_returns_common_error_response_when_authentication_is_rejected(t *testing.T) {
	// Given
	requestID := uuid.NewString()
	router := chi.NewRouter()
	RegisterExtAuthzRoutes(router, NewExtAuthz(characterizationKeys(t, time.Now()), &fixedStatusChecker{state: StatusActive}))
	request := httptest.NewRequest(http.MethodGet, "/internal/ext-authz/orders", nil)
	request.Header.Set("X-Request-Id", requestID)
	request.Header.Set("Authorization", "Bearer secret-invalid-token")
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Equal(t, "application/json", response.Header().Get("Content-Type"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	requireCommonExtAuthzError(t, response, map[string]any{
		"status":    float64(http.StatusUnauthorized),
		"code":      "AUTH_SESSION_REQUIRED",
		"message":   "인증 정보를 확인한 뒤 다시 시도해주세요.",
		"requestId": requestID,
	})
	require.NotContains(t, response.Body.String(), "secret-invalid-token")
}

func Test_ExtAuthz_returns_common_error_response_when_session_state_is_unavailable(t *testing.T) {
	// Given
	requestID := uuid.NewString()
	keys := characterizationKeys(t, time.Now())
	token, _, err := keys.SignAccessToken(uuid.NewString(), uuid.NewString(), time.Minute)
	require.NoError(t, err)
	router := chi.NewRouter()
	RegisterExtAuthzRoutes(router, NewExtAuthz(keys, &fixedStatusChecker{state: StatusUnavailable}))
	request := httptest.NewRequest(http.MethodGet, "/internal/ext-authz/payments", nil)
	request.Header.Set("X-Request-Id", requestID)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	requireCommonExtAuthzError(t, response, map[string]any{
		"status":    float64(http.StatusServiceUnavailable),
		"code":      "AUTH_SERVICE_UNAVAILABLE",
		"message":   "인증 서비스를 일시적으로 사용할 수 없습니다.",
		"requestId": requestID,
	})
	require.NotContains(t, response.Body.String(), token)
}

func requireCommonExtAuthzError(t *testing.T, response *httptest.ResponseRecorder, expected map[string]any) {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, expected, body)
	require.Empty(t, identityResponseHeaders(response.Header()))
}
