package jwks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationjwks "github.com/Medikong/services/services/auth-service/internal/application/jwks"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
)

type Controller struct {
	provider applicationjwks.Provider
}

func NewController(provider applicationjwks.Provider) *Controller {
	return &Controller{provider: provider}
}

func (c *Controller) Get(w http.ResponseWriter, r *http.Request) {
	jwks, err := c.provider.JWKS()
	if err != nil {
		writeUnavailable(w, r)
		return
	}
	body, err := json.Marshal(jwks)
	if err != nil {
		writeUnavailable(w, r)
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

func writeUnavailable(w http.ResponseWriter, r *http.Request) {
	httputil.WriteError(w, r, failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", "잠시 후 다시 시도해주세요."))
}
