package httpmiddleware

import (
	"context"
	"net/http"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
)

type principalContextKey struct{}

func RequirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
		if err != nil || value.Type != principal.TypeUser || value.UserID == "" {
			httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal").
				Public("사용자 Principal 정보가 필요합니다.").
				New("user principal is required"))
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, value)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Principal(r.Context()).HasRole(role) {
				httpapi.WriteError(w, r, httpapi.Forbidden("auth.forbidden").
					Public("요청을 수행할 권한이 없습니다.").
					New("required role is missing"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func Principal(ctx context.Context) principal.Principal {
	value, _ := ctx.Value(principalContextKey{}).(principal.Principal)
	return value
}
