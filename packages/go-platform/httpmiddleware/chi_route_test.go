package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestChiRoutePatternPreMatchesTemplateWithoutRawPathFallback(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/orders/{orderId}", func(http.ResponseWriter, *http.Request) {})
	resolve := ChiRoutePattern(router)

	request := httptest.NewRequest(http.MethodGet, "/orders/private-order-id", nil)
	if got := resolve(request); got != "/orders/{orderId}" {
		t.Fatalf("route pattern = %q, want template", got)
	}
	unknown := httptest.NewRequest(http.MethodGet, "/private/raw/path", nil)
	if got := resolve(unknown); got != "unmatched" {
		t.Fatalf("unknown route pattern = %q, want unmatched", got)
	}
	if got := ChiRoutePattern(nil)(request); got != "unmatched" {
		t.Fatalf("nil routes pattern = %q, want unmatched", got)
	}
}
