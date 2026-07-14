package httpmiddleware

import (
	"net/http"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func RequestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _, err := requestcontext.Ensure(r)
		if err != nil {
			httpapi.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, request)
	})
}
