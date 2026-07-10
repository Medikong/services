package http

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/bsm/redislock"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"

	"github.com/Medikong/services/packages/go-audit"
	authzmiddleware "github.com/Medikong/services/packages/go-authz/httpmiddleware"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
	"github.com/Medikong/services/services/go-reference-service/internal/domain/sample"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/config"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/observability"
)

var resourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func NewRouter(
	cfg config.ServerConfig,
	service sample.Service,
	redisClient *redis.Client,
	healthState *operational.Handler,
	metrics *observability.Metrics,
) (http.Handler, error) {
	lockMiddleware, err := platformmiddleware.RedisLock(platformmiddleware.RedisLockConfig{
		Client:   redislock.New(redisClient),
		Redis:    redisClient,
		Key:      lockKey,
		Policy:   cfg.Lock,
		OnResult: metrics.RecordLock,
	})
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return platformmiddleware.Stack(platformmiddleware.Config{
			ServiceName:  cfg.Service.Name,
			RoutePattern: RoutePattern,
		}, next)
	})
	router.Use(platformmiddleware.Timeout(cfg.HTTP.RequestTimeout))
	router.Use(healthState.RejectWhileDraining)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.NotFound("common.not_found", "요청한 API를 찾을 수 없습니다."))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.MethodNotAllowed("common.method_not_allowed", "허용되지 않은 HTTP 메서드입니다."))
	})

	handler := Handler{service: service, metrics: metrics}
	router.With(authzmiddleware.RequirePrincipal, authzmiddleware.RequireRole("customer"), RequireIdempotencyKey, lockMiddleware).
		Post("/v1/reference/resources/{resourceID}/audit", handler.Apply)
	return router, nil
}

type Handler struct {
	service sample.Service
	metrics *observability.Metrics
}

func (h Handler) Apply(w http.ResponseWriter, r *http.Request) {
	resourceID := chi.URLParam(r, "resourceID")
	fence := platformmiddleware.FencingToken(r.Context())
	value := authzmiddleware.Principal(r.Context())
	span := trace.SpanContextFromContext(r.Context())
	err := h.service.Apply(r.Context(), sample.Command{
		ResourceID:     resourceID,
		FenceToken:     fence,
		Actor:          audit.Actor{Type: string(value.Type), ID: value.UserID},
		RequestID:      requestcontext.RequestID(r.Context()),
		IdempotencyKey: r.Header.Get(headers.IdempotencyKey),
		TraceID:        validTraceID(span),
		SpanID:         validSpanID(span),
	})
	if err != nil {
		switch {
		case errors.Is(err, sample.ErrStaleFence):
			h.metrics.RecordOperation("stale_fence")
			httpapi.WriteError(w, r, httpapi.Conflict(
				"reference.stale_fence",
				"더 최신 요청이 이미 반영되었습니다.",
			))
			return
		case errors.Is(err, sample.ErrDuplicateOperation):
			h.metrics.RecordOperation("duplicate")
			httpapi.WriteError(w, r, httpapi.Conflict(
				"reference.duplicate_operation",
				"같은 요청이 이미 처리되었습니다.",
			))
			return
		}
		h.metrics.RecordOperation("error")
		httpapi.WriteError(w, r, httpapi.Internal(err))
		return
	}
	h.metrics.RecordOperation("success")
	w.Header().Set("X-Fencing-Token", strconv.FormatInt(fence, 10))
	w.WriteHeader(http.StatusNoContent)
}

func RoutePattern(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func lockKey(r *http.Request) (platformmiddleware.RedisLockKey, error) {
	resourceID := chi.URLParam(r, "resourceID")
	if !resourceIDPattern.MatchString(resourceID) {
		return platformmiddleware.RedisLockKey{}, httpapi.BadRequest(
			"reference.invalid_resource_id",
			"resourceID 형식이 올바르지 않습니다.",
		)
	}
	// Both keys share a Redis Cluster hash tag.
	return platformmiddleware.RedisLockKey{
		Lock:  "reference:{" + resourceID + "}:lock",
		Fence: "reference:{" + resourceID + "}:fence",
	}, nil
}

func validTraceID(span trace.SpanContext) string {
	if !span.IsValid() {
		return ""
	}
	return span.TraceID().String()
}

func validSpanID(span trace.SpanContext) string {
	if !span.IsValid() {
		return ""
	}
	return span.SpanID().String()
}
