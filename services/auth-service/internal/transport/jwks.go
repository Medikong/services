package http

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
)

func RegisterJWKSRoute(router chi.Router, handler http.HandlerFunc) {
	router.Get("/.well-known/jwks.json", handler)
}

func NewJWKSHandler(keys security.Keys) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jwks, err := keys.JWKS()
		if err != nil {
			writeJWKSUnavailable(w, r)
			return
		}
		body, err := json.Marshal(jwks)
		if err != nil {
			writeJWKSUnavailable(w, r)
			return
		}
		etag := fmt.Sprintf("\"%x\"", sha256.Sum256(body))
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(body)
	}
}

func writeJWKSUnavailable(w http.ResponseWriter, r *http.Request) {
	httputil.WriteError(w, r, httpapi.Error(http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE").
		Public("잠시 후 다시 시도해주세요.").
		New("JWKS unavailable"))
}
