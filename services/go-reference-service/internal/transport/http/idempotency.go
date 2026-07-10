package http

import (
	"net/http"
	"strings"

	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
)

func RequireIdempotencyKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get(headers.IdempotencyKey))
		if key == "" || len(key) > 128 {
			httpapi.WriteError(w, r, httpapi.BadRequest(
				"common.invalid_idempotency_key",
				"Idempotency-Key 헤더가 필요하며 128자 이하여야 합니다.",
			))
			return
		}
		r.Header.Set(headers.IdempotencyKey, key)
		next.ServeHTTP(w, r)
	})
}
