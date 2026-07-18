package session

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fixedStatusChecker struct {
	state    StatusState
	check    StatusCheck
	deadline time.Time
	calls    int
}

func (c *fixedStatusChecker) Check(ctx context.Context, check StatusCheck) StatusState {
	c.calls++
	c.check = check
	c.deadline, _ = ctx.Deadline()
	return c.state
}

type readTrackingBody struct {
	reader *strings.Reader
	read   bool
}

func (b *readTrackingBody) Read(payload []byte) (int, error) {
	b.read = true
	return b.reader.Read(payload)
}

func (*readTrackingBody) Close() error { return nil }

func Test_ExtAuthz_returns_only_verified_identity_headers_when_bearer_session_is_active(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	userID, sessionID := uuid.New(), uuid.New()
	keys := characterizationKeys(t, now)
	token, _, err := keys.SignAccessToken(userID.String(), sessionID.String(), time.Minute)
	require.NoError(t, err)
	claims, err := keys.VerifyAccessToken(token)
	require.NoError(t, err)
	checker := &fixedStatusChecker{state: StatusActive}
	router := chi.NewRouter()
	RegisterExtAuthzRoutes(router, NewExtAuthz(keys, checker))
	body := &readTrackingBody{reader: strings.NewReader("must not be consumed")}
	request := httptest.NewRequest(http.MethodPost, "/internal/ext-authz/orders/ord-1", body)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-User-Id", uuid.NewString())
	request.Header.Set("X-Session-Id", uuid.NewString())
	request.Header.Set("X-Token-Id", uuid.NewString())
	request.Header.Set("X-User-Role", "admin")
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, userID.String(), response.Header().Get("X-User-Id"))
	require.Equal(t, sessionID.String(), response.Header().Get("X-Session-Id"))
	require.Equal(t, claims.TokenID, response.Header().Get("X-Token-Id"))
	require.Equal(t, StatusCheck{UserID: userID, SessionID: sessionID, TokenID: uuid.MustParse(claims.TokenID)}, checker.check)
	require.Equal(t, 1, checker.calls)
	require.WithinDuration(t, time.Now().Add(extAuthzTimeout), checker.deadline, 100*time.Millisecond)
	require.Empty(t, response.Body.Bytes())
	require.False(t, body.read)
	require.Equal(t, []string{"X-Session-Id", "X-Token-Id", "X-User-Id"}, identityResponseHeaders(response.Header()))
}

type deadlineStatusChecker struct{}

func (deadlineStatusChecker) Check(ctx context.Context, _ StatusCheck) StatusState {
	<-ctx.Done()
	return StatusActive
}

func Test_ExtAuthz_returns_service_unavailable_at_request_budget_when_dependency_hangs(t *testing.T) {
	// Given
	keys := characterizationKeys(t, time.Now())
	token, _, err := keys.SignAccessToken(uuid.NewString(), uuid.NewString(), time.Minute)
	require.NoError(t, err)
	router := chi.NewRouter()
	RegisterExtAuthzRoutes(router, NewExtAuthz(keys, deadlineStatusChecker{}))
	request := httptest.NewRequest(http.MethodGet, "/internal/ext-authz/orders", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	started := time.Now()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.GreaterOrEqual(t, time.Since(started), extAuthzTimeout)
	require.Less(t, time.Since(started), extAuthzTimeout+150*time.Millisecond)
	require.Empty(t, identityResponseHeaders(response.Header()))
}

func Test_ExtAuthz_returns_unauthorized_when_verified_subject_mismatches_active_session(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	keys := characterizationKeys(t, now)
	sessionID := uuid.New()
	token, _, err := keys.SignAccessToken(uuid.NewString(), sessionID.String(), time.Minute)
	require.NoError(t, err)
	record := StatusRecord{
		UserID: uuid.New(), SessionID: sessionID, State: StatusActive,
		AbsoluteExpiresAt: now.Add(time.Hour), Version: 1,
	}
	statuses := NewStatusService(StatusServiceOptions{
		Cache: &recordingStatusCache{record: record}, Source: &recordingStatusSource{},
		Now: func() time.Time { return now },
		Config: StatusServiceConfig{
			ActiveTTL: 5 * time.Minute, AccessTTL: 15 * time.Minute,
			FallbackTimeout: 100 * time.Millisecond, MaxFallbacks: 1,
		},
	})
	router := chi.NewRouter()
	RegisterExtAuthzRoutes(router, NewExtAuthz(keys, statuses))
	request := httptest.NewRequest(http.MethodGet, "/internal/ext-authz/orders", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Empty(t, identityResponseHeaders(response.Header()))
}

func Test_ExtAuthz_returns_unauthorized_without_identity_headers_when_authentication_is_rejected(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	userID, sessionID := uuid.New(), uuid.New()
	keys := characterizationKeys(t, now)
	token, _, err := keys.SignAccessToken(userID.String(), sessionID.String(), time.Minute)
	require.NoError(t, err)
	expiredKeys := keys
	expiredKeys.Now = func() time.Time { return now.Add(2 * time.Minute) }
	tests := []struct {
		name      string
		keys      security.Keys
		authority []string
		state     StatusState
	}{
		{name: "missing bearer", keys: keys, state: StatusActive},
		{name: "basic credential", keys: keys, authority: []string{"Basic abc"}, state: StatusActive},
		{name: "duplicate authorization", keys: keys, authority: []string{"Bearer " + token, "Bearer " + token}, state: StatusActive},
		{name: "invalid signature", keys: keys, authority: []string{"Bearer " + token[:len(token)-1] + "x"}, state: StatusActive},
		{name: "expired bearer", keys: expiredKeys, authority: []string{"Bearer " + token}, state: StatusActive},
		{name: "revoked session", keys: keys, authority: []string{"Bearer " + token}, state: StatusRevoked},
		{name: "expired session", keys: keys, authority: []string{"Bearer " + token}, state: StatusExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker := &fixedStatusChecker{state: test.state}
			router := chi.NewRouter()
			RegisterExtAuthzRoutes(router, NewExtAuthz(test.keys, checker))
			request := httptest.NewRequest(http.MethodGet, "/internal/ext-authz/catalog/items/1", nil)
			for _, value := range test.authority {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()

			// When
			router.ServeHTTP(response, request)

			// Then
			require.Equal(t, http.StatusUnauthorized, response.Code)
			require.Empty(t, identityResponseHeaders(response.Header()))
			require.NotContains(t, response.Body.String(), token)
		})
	}
}

func Test_ExtAuthz_returns_service_unavailable_without_identity_headers_when_session_state_is_indeterminate(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	keys := characterizationKeys(t, now)
	token, _, err := keys.SignAccessToken(uuid.NewString(), uuid.NewString(), time.Minute)
	require.NoError(t, err)
	router := chi.NewRouter()
	RegisterExtAuthzRoutes(router, NewExtAuthz(keys, &fixedStatusChecker{state: StatusUnavailable}))
	request := httptest.NewRequest(http.MethodPatch, "/internal/ext-authz/payments/pay-1", bytes.NewBufferString("ignored"))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	// When
	router.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Empty(t, identityResponseHeaders(response.Header()))
	require.NotContains(t, response.Body.String(), token)
}

func identityResponseHeaders(header http.Header) []string {
	result := make([]string, 0, 3)
	for _, name := range []string{"X-Session-Id", "X-Token-Id", "X-User-Id", "X-User-Role"} {
		if header.Get(name) != "" {
			result = append(result, name)
		}
	}
	return result
}
