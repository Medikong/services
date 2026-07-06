package httpmiddleware

import (
	"net/http"

	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func ResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestID := requestcontext.RequestID(r.Context()); requestID != "" {
			w.Header().Set(requestcontext.RequestIDHeader, requestID)
		}
		next.ServeHTTP(w, r)
	})
}
