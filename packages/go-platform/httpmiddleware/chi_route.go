package httpmiddleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ChiRoutePattern resolves a bounded route template both before and after chi
// dispatch. Unknown routes never fall back to the raw request path.
func ChiRoutePattern(routes chi.Routes) func(*http.Request) string {
	return func(request *http.Request) string {
		if request == nil {
			return "unmatched"
		}
		if current := chi.RouteContext(request.Context()); current != nil {
			if pattern := current.RoutePattern(); pattern != "" {
				return pattern
			}
		}
		if routes == nil {
			return "unmatched"
		}
		match := chi.NewRouteContext()
		if !routes.Match(match, request.Method, request.URL.Path) {
			return "unmatched"
		}
		if pattern := match.RoutePattern(); pattern != "" {
			return pattern
		}
		return "unmatched"
	}
}
