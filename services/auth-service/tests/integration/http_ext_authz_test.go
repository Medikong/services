//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type extAuthzSessionFixture struct {
	userID     uuid.UUID
	identityID uuid.UUID
	linkID     uuid.UUID
	sessionID  uuid.UUID
	state      string
}

func newExtAuthzSessionFixture(state string) extAuthzSessionFixture {
	return extAuthzSessionFixture{
		userID: uuid.New(), identityID: uuid.New(), linkID: uuid.New(), sessionID: uuid.New(), state: state,
	}
}

func (f extAuthzSessionFixture) seed(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	seedRefreshPrincipal(t, ctx, db, f.userID, f.identityID, f.linkID)
	revokedAt, reason := any(nil), any(nil)
	if f.state == "revoked" {
		revokedAt, reason = time.Now().UTC(), "integration_test"
	}
	_, err := db.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, identity_link_id, authentication_method,
			session_status, client_channel, issued_at, idle_expires_at, absolute_expires_at,
			revoked_at, revocation_reason
		) VALUES ($1, $2, $3, $4, 'email_password', $5, 'web', now(), now() + interval '20 minutes',
			now() + interval '1 hour', $6, $7)
	`, f.sessionID, f.userID, f.identityID, f.linkID, f.state, revokedAt, reason)
	require.NoError(t, err)
}

func Test_ExtAuthz_real_HTTP_Postgres_and_Redis_surface(t *testing.T) {
	// Given
	harness := newProductionHTTPHarness(t)
	keys := security.Keys{
		JWTKey:       []byte(integrationJWTPrivateKeyPEM(t)),
		JWTKeyID:     "http-e2e-key",
		JWTIssuer:    httpE2EJWTIssuer,
		JWTAudiences: []string{"dropmong-api"},
	}
	active := newExtAuthzSessionFixture("active")
	revoked := newExtAuthzSessionFixture("revoked")
	unavailable := newExtAuthzSessionFixture("active")
	for _, fixture := range []extAuthzSessionFixture{active, revoked, unavailable} {
		fixture.seed(t, harness.ctx, harness.db)
	}
	activeToken, _, err := keys.SignAccessToken(active.userID.String(), active.sessionID.String(), time.Minute)
	require.NoError(t, err)
	activeClaims, err := keys.VerifyAccessToken(activeToken)
	require.NoError(t, err)
	revokedToken, _, err := keys.SignAccessToken(revoked.userID.String(), revoked.sessionID.String(), time.Minute)
	require.NoError(t, err)
	unavailableToken, _, err := keys.SignAccessToken(unavailable.userID.String(), unavailable.sessionID.String(), time.Minute)
	require.NoError(t, err)

	// When
	activeResponse := harness.do(httpE2ERequest{
		Method: http.MethodPost,
		Path:   "/internal/ext-authz/orders/ord-1",
		Headers: http.Header{
			"X-User-Id":    []string{uuid.NewString()},
			"X-Session-Id": []string{uuid.NewString()},
			"X-Token-Id":   []string{uuid.NewString()},
			"X-User-Role":  []string{"admin"},
		},
		Credentials: httpE2ECredentials{AccessToken: activeToken},
	})

	// Then
	assertHTTPNoContent(t, activeResponse, http.StatusOK)
	require.Equal(t, active.userID.String(), activeResponse.header.Get("X-User-Id"))
	require.Equal(t, active.sessionID.String(), activeResponse.header.Get("X-Session-Id"))
	require.Equal(t, activeClaims.TokenID, activeResponse.header.Get("X-Token-Id"))
	require.Equal(t, []string{"X-Session-Id", "X-Token-Id", "X-User-Id"}, extAuthzIdentityHeaders(activeResponse.header))
	assertResponseOmits(t, activeResponse, activeToken)

	// When
	invalidResponse := harness.do(httpE2ERequest{
		Method: http.MethodGet, Path: "/internal/ext-authz/catalog/items/1",
		Credentials: httpE2ECredentials{AccessToken: activeToken + "x"},
	})
	revokedResponse := harness.do(httpE2ERequest{
		Method: http.MethodDelete, Path: "/internal/ext-authz/payments/pay-1",
		Credentials: httpE2ECredentials{AccessToken: revokedToken},
	})

	// Then
	decodeHTTPError(t, invalidResponse, http.StatusUnauthorized, "AUTH_SESSION_REQUIRED")
	decodeHTTPError(t, revokedResponse, http.StatusUnauthorized, "AUTH_SESSION_REQUIRED")
	require.Empty(t, extAuthzIdentityHeaders(invalidResponse.header))
	require.Empty(t, extAuthzIdentityHeaders(revokedResponse.header))
	assertResponseOmits(t, invalidResponse, activeToken)
	assertResponseOmits(t, revokedResponse, revokedToken)

	// When
	shutdownRedis(t, harness.redisURL)
	unavailableResponse := harness.do(httpE2ERequest{
		Method: http.MethodPatch, Path: "/internal/ext-authz/orders/ord-2",
		Credentials: httpE2ECredentials{AccessToken: unavailableToken},
	})

	// Then
	decodeHTTPError(t, unavailableResponse, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE")
	require.Empty(t, extAuthzIdentityHeaders(unavailableResponse.header))
	assertResponseOmits(t, unavailableResponse, unavailableToken)
}

func shutdownRedis(t *testing.T, rawURL string) {
	t.Helper()
	options, err := redis.ParseURL(rawURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = client.Do(ctx, "SHUTDOWN", "NOSAVE").Err()
}

func extAuthzIdentityHeaders(header http.Header) []string {
	result := make([]string, 0, 3)
	for name := range header {
		if name == "X-User-Id" || name == "X-Session-Id" || name == "X-Token-Id" || name == "X-User-Role" {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}
