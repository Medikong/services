package http

import (
	"context"
	"net/http"
	"testing"
	"time"

	appoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProductionRouterContainsEveryProductionContractPathAndNoDevelopmentPath(t *testing.T) {
	router := newRouterForContractTest(t, false)
	routes := routeSet(t, router)
	want := productionContractRoutes()
	if len(routes) != len(want) {
		t.Fatalf("production route count = %d, want %d: %#v", len(routes), len(want), routes)
	}
	for route := range want {
		if !routes[route] {
			t.Errorf("missing production route %s", route)
		}
	}
	for route := range routes {
		if route.path == "/api/v1/dev/auth/verification-messages/{challengeId}" {
			t.Errorf("production router exposes development route")
		}
	}
}

func TestDevelopmentRouterAddsOnlyDevelopmentContractPath(t *testing.T) {
	router := newRouterForContractTest(t, true)
	routes := routeSet(t, router)
	if len(routes) != len(productionContractRoutes())+1 {
		t.Fatalf("development route count = %d, want %d", len(routes), len(productionContractRoutes())+1)
	}
	if !routes[routeKey{method: "GET", path: "/api/v1/dev/auth/verification-messages/{challengeId}"}] {
		t.Fatal("development router is missing virtual message route")
	}
}

func TestRouterAcceptsExplicitApprovalPort(t *testing.T) {
	cfg, pool := routerContractFixture(t, false)
	router, err := NewRouterWithOptions(cfg, pool, nil, nil, RouterOptions{
		ApprovalPort: appoperator.StaticApprovalPort{Allow: true},
	})
	if err != nil {
		t.Fatalf("NewRouterWithOptions: %v", err)
	}
	if router == nil {
		t.Fatal("NewRouterWithOptions returned a nil router")
	}
}

type routeKey struct{ method, path string }

func routeSet(t *testing.T, handler any) map[routeKey]bool {
	t.Helper()
	routes, ok := handler.(chi.Routes)
	if !ok {
		t.Fatal("router does not implement chi.Routes")
	}
	result := map[routeKey]bool{}
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		result[routeKey{method, route}] = true
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	return result
}
func newRouterForContractTest(t *testing.T, development bool) any {
	t.Helper()
	cfg, pool := routerContractFixture(t, development)
	router, err := NewRouter(cfg, pool, nil, nil)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func routerContractFixture(t *testing.T, development bool) (config.ServerConfig, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://app:app@127.0.0.1:1/auth?sslmode=disable")
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	cfg := config.ServerConfig{Auth: config.AuthConfig{CredentialHMACKey: "01234567890123456789012345678901", ReplayEncryptionKey: "01234567890123456789012345678901", JWTSecret: "01234567890123456789012345678901", JWTIssuer: "auth-test", IntentTTL: time.Minute, RegistrationTTL: time.Minute, ChallengeTTL: time.Minute, SessionTTL: time.Hour, RefreshTTL: time.Hour, AccessTTL: time.Minute, ProofTTL: time.Minute, SessionCookieName: "__Host-dm_session", AuthFlowCookieName: "__Host-dm_auth"}, Development: config.DevelopmentConfig{Enabled: development, RouteEnabled: development, VirtualAdaptersEnabled: development, AccessToken: "test-development-token", VirtualMessageKey: "01234567890123456789012345678901"}}
	return cfg, pool
}
func productionContractRoutes() map[routeKey]bool {
	items := []routeKey{{"POST", "/api/v1/auth/intents"}, {"GET", "/api/v1/auth/methods"}, {"POST", "/api/v1/auth/registrations"}, {"POST", "/api/v1/auth/registrations/{registrationId}/challenges"}, {"POST", "/api/v1/auth/registrations/{registrationId}/challenges/{challengeId}/verify"}, {"POST", "/api/v1/auth/registrations/{registrationId}/complete"}, {"POST", "/api/v1/auth/signins/email"}, {"POST", "/api/v1/auth/signins/phone/challenges"}, {"POST", "/api/v1/auth/signins/phone/challenges/{challengeId}/verify"}, {"POST", "/api/v1/auth/password-resets"}, {"POST", "/api/v1/auth/password-resets/{passwordResetId}/challenges"}, {"POST", "/api/v1/auth/password-resets/{passwordResetId}/challenges/{challengeId}/verify"}, {"PUT", "/api/v1/auth/password-resets/{passwordResetId}/password"}, {"POST", "/api/v1/auth/sessions/refresh"}, {"POST", "/api/v1/auth/sessions/logout"}, {"GET", "/api/v1/auth/context"}, {"POST", "/api/v1/auth/reauthentications/email"}, {"POST", "/api/v1/auth/method-links"}, {"POST", "/api/v1/auth/method-links/{linkIntentId}/challenges"}, {"POST", "/api/v1/auth/method-links/{linkIntentId}/complete"}, {"POST", "/api/v1/auth/phone-replacements"}, {"POST", "/api/v1/auth/phone-replacements/{replacementId}/challenges"}, {"POST", "/api/v1/auth/phone-replacements/{replacementId}/complete"}, {"GET", "/api/v1/operator/auth/users/{userId}"}, {"GET", "/api/v1/operator/auth/policies"}, {"PATCH", "/api/v1/operator/auth/policies/{policyName}"}, {"POST", "/api/v1/operator/auth/manual-actions"}, {"GET", "/api/v1/auth/registrations/{registrationId}"}, {"POST", "/api/v1/auth/intents/{intentId}/action-resume"}}
	result := map[routeKey]bool{}
	for _, item := range items {
		result[item] = true
	}
	return result
}
