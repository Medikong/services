package user

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
)

const (
	createUserPath     = "/users"
	getOwnProfilePath  = "/users/me/profile"
	profileImagePath   = "/users/me/profile-image"
	changeStatusPath   = "/operator/users/{userId}/status-transitions"
	getStatusPath      = "/operator/users/{userId}/status"
	csrfVerifiedHeader = "X-Csrf-Verified"
)

type RouteContract struct {
	Method string
	Path   string
}

var BusinessRoutes = []RouteContract{
	{Method: http.MethodPost, Path: "/api/v1" + createUserPath},
	{Method: http.MethodGet, Path: "/api/v1" + getOwnProfilePath},
	{Method: http.MethodPatch, Path: "/api/v1" + getOwnProfilePath},
	{Method: http.MethodPut, Path: "/api/v1" + profileImagePath},
	{Method: http.MethodPost, Path: "/api/v1" + changeStatusPath},
	{Method: http.MethodGet, Path: "/api/v1" + getStatusPath},
}

type principalContextKey struct{}

func RegisterRoutes(router chi.Router, handler *UserHandler) {
	router.With(handler.requireAllowedOrigin).Post(createUserPath, handler.CreateUser)
	router.Group(func(own chi.Router) {
		own.Use(requirePrincipal)
		own.Get(getOwnProfilePath, handler.GetOwnProfile)
		own.With(handler.requireAllowedOrigin).Patch(getOwnProfilePath, handler.UpdateOwnProfile)
		own.With(handler.requireAllowedOrigin).Put(profileImagePath, handler.UpdateOwnProfileImage)
	})
	router.Group(func(operator chi.Router) {
		operator.Use(requirePrincipal)
		operator.With(handler.requireOperator("user.account_status.change", true)).Post(changeStatusPath, handler.ChangeUserAccountStatus)
		operator.With(handler.requireOperator("user.account_status.read", false)).Get(getStatusPath, handler.GetUserAccountStatus)
	})
}

func requirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
		if err != nil || value.Type != principal.TypeUser || value.UserID == "" {
			httpapi.WriteError(w, r, httpapi.Unauthorized("USER_AUTHENTICATION_REQUIRED").
				Public("사용자 인증이 필요합니다.").
				New("user authentication is required"))
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, value)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestPrincipal(ctx context.Context) principal.Principal {
	value, _ := ctx.Value(principalContextKey{}).(principal.Principal)
	return value
}

func (h *UserHandler) requireAllowedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.allowedOrigins[strings.TrimSpace(r.Header.Get("Origin"))]; !ok {
			httpapi.WriteError(w, r, httpapi.Forbidden("USER_FORBIDDEN").
				Public("허용되지 않은 요청 출처입니다.").
				New("request origin is not allowed"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *UserHandler) requireOperator(permission string, mutation bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := requestPrincipal(r.Context())
			code := "USER_FORBIDDEN"
			if mutation {
				code = "USER_ACCOUNT_STATUS_PERMISSION_DENIED"
			}
			if value.SessionID == "" || value.AuthLevel != "strong" || !value.HasRole(permission) {
				httpapi.WriteError(w, r, httpapi.Forbidden(code).
					Public("계정 상태를 조회하거나 변경할 권한이 없습니다.").
					New("operator permission is required"))
				return
			}
			if mutation {
				_, originAllowed := h.allowedOrigins[strings.TrimSpace(r.Header.Get("Origin"))]
				if !originAllowed || !strings.EqualFold(strings.TrimSpace(r.Header.Get(csrfVerifiedHeader)), "true") {
					httpapi.WriteError(w, r, httpapi.Forbidden(code).
						Public("운영 요청의 출처 또는 CSRF 검증이 유효하지 않습니다.").
						New("operator origin or CSRF verification is invalid"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
