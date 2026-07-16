package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/domain/authentication"
	"github.com/Medikong/services/services/auth-service/internal/domain/development"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
)

func TestProductionRouterContainsEveryProductionPathAndNoDevelopmentPath(t *testing.T) {
	router, _ := newRouterForTest(t, false)
	routes := routeSet(t, router)
	want := productionRoutes()
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

func TestDevelopmentRouterAddsOnlyDevelopmentPath(t *testing.T) {
	router, _ := newRouterForTest(t, true)
	routes := routeSet(t, router)
	if len(routes) != len(productionRoutes())+1 {
		t.Fatalf("development route count = %d, want %d", len(routes), len(productionRoutes())+1)
	}
	if !routes[routeKey{method: http.MethodGet, path: "/api/v1/dev/auth/verification-messages/{challengeId}"}] {
		t.Fatal("development router is missing virtual message route")
	}
}

func TestRouterAppliesDrainingMiddleware(t *testing.T) {
	router, health := newRouterForTest(t, false)
	health.BeginDrain()
	request := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("X-Request-Id") == "" {
		t.Fatal("request ID middleware was not applied")
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func TestRouterRejectsMissingHandlers(t *testing.T) {
	health := operational.New("auth-service", nil)
	if _, err := NewRouter(RouterConfig{ServiceName: "auth-service", RequestTimeout: time.Second}, health, Controllers{}); err == nil {
		t.Fatal("NewRouter() error = nil")
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

func newRouterForTest(t *testing.T, developmentRoutes bool) (http.Handler, *operational.Handler) {
	t.Helper()
	health := operational.New("auth-service", nil)
	router, err := NewRouter(RouterConfig{
		ServiceName:              "auth-service",
		RequestTimeout:           time.Second,
		DevelopmentRoutesEnabled: developmentRoutes,
	}, health, Controllers{
		Bootstrap:     &intent.BootstrapController{},
		SignIn:        &authentication.SignInController{},
		Session:       &session.SessionController{},
		Registration:  &registration.RegistrationController{},
		PasswordReset: &passwordreset.PasswordResetController{},
		Identity:      &identity.IdentityManagementController{},
		Operator:      &operator.OperatorController{},
		ActionResume:  &intent.ActionResumeController{},
		UserAuthState: &userauthstate.UserAuthStateController{},
		Development:   &development.DevelopmentController{},
		JWKS: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router, health
}

func productionRoutes() map[routeKey]bool {
	items := []routeKey{
		{http.MethodGet, "/.well-known/jwks.json"},
		{http.MethodPost, "/api/v1/auth/intents"},
		{http.MethodGet, "/api/v1/auth/methods"},
		{http.MethodPost, "/api/v1/auth/registrations"},
		{http.MethodPost, "/api/v1/auth/registrations/{registrationId}/challenges"},
		{http.MethodPost, "/api/v1/auth/registrations/{registrationId}/challenges/{challengeId}/verify"},
		{http.MethodPost, "/api/v1/auth/registrations/{registrationId}/complete"},
		{http.MethodPost, "/api/v1/auth/signins/email"},
		{http.MethodPost, "/api/v1/auth/signins/phone/challenges"},
		{http.MethodPost, "/api/v1/auth/signins/phone/challenges/{challengeId}/verify"},
		{http.MethodPost, "/api/v1/auth/password-resets"},
		{http.MethodPost, "/api/v1/auth/password-resets/{passwordResetId}/challenges"},
		{http.MethodPost, "/api/v1/auth/password-resets/{passwordResetId}/challenges/{challengeId}/verify"},
		{http.MethodPut, "/api/v1/auth/password-resets/{passwordResetId}/password"},
		{http.MethodPost, "/api/v1/auth/sessions/refresh"},
		{http.MethodPost, "/api/v1/auth/sessions/logout"},
		{http.MethodGet, "/api/v1/auth/context"},
		{http.MethodPost, "/api/v1/auth/reauthentications/email"},
		{http.MethodPost, "/api/v1/auth/method-links"},
		{http.MethodPost, "/api/v1/auth/method-links/{linkIntentId}/challenges"},
		{http.MethodPost, "/api/v1/auth/method-links/{linkIntentId}/complete"},
		{http.MethodPost, "/api/v1/auth/phone-replacements"},
		{http.MethodPost, "/api/v1/auth/phone-replacements/{replacementId}/challenges"},
		{http.MethodPost, "/api/v1/auth/phone-replacements/{replacementId}/complete"},
		{http.MethodGet, "/api/v1/operator/auth/users/{userId}"},
		{http.MethodGet, "/api/v1/operator/auth/policies"},
		{http.MethodPatch, "/api/v1/operator/auth/policies/{policyName}"},
		{http.MethodPost, "/api/v1/operator/auth/manual-actions"},
		{http.MethodPut, "/api/v1/operator/auth/users/{userId}/account-status"},
		{http.MethodGet, "/api/v1/auth/registrations/{registrationId}"},
		{http.MethodPost, "/api/v1/auth/intents/{intentId}/action-resume"},
	}
	result := make(map[routeKey]bool, len(items))
	for _, item := range items {
		result[item] = true
	}
	return result
}
