package operational

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"sort"
	"sync/atomic"
	"time"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

const (
	metricsContentType  = "text/plain; version=0.0.4; charset=utf-8"
	defaultCheckTimeout = 2 * time.Second
)

type Check func(ctx context.Context) error

type Config struct {
	Service          string
	ReadinessTimeout time.Duration
	Checks           map[string]Check
	Metrics          http.Handler
	SetReady         func(bool)
}

type Handler struct {
	service          string
	timeout          time.Duration
	checks           map[string]Check
	metrics          http.Handler
	setReady         func(bool)
	metricCollectors []func(io.Writer)
	now              func() time.Time
	draining         atomic.Bool
}

func New(service string, checks map[string]Check) *Handler {
	return NewHandler(Config{Service: service, Checks: checks})
}

func NewWithMetrics(service string, checks map[string]Check, collectors []func(io.Writer)) *Handler {
	handler := NewHandler(Config{Service: service, Checks: checks})
	handler.metricCollectors = collectors
	return handler
}

func NewHandler(config Config) *Handler {
	if config.Checks == nil {
		config.Checks = map[string]Check{}
	}
	if config.ReadinessTimeout <= 0 {
		config.ReadinessTimeout = defaultCheckTimeout
	}
	return &Handler{
		service:  config.Service,
		timeout:  config.ReadinessTimeout,
		checks:   config.Checks,
		metrics:  config.Metrics,
		setReady: config.SetReady,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)
	mux.HandleFunc("GET /metrics", h.Metrics)
}

func (h *Handler) RegisterAll(mux *http.ServeMux, pprofEnabled bool) {
	h.Register(mux)
	if !pprofEnabled {
		return
	}
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}

func (h *Handler) BeginDrain() {
	h.draining.Store(true)
	if h.setReady != nil {
		h.setReady(false)
	}
}

func (h *Handler) Draining() bool {
	return h.draining.Load()
}

func (h *Handler) RejectWhileDraining(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Draining() {
			w.Header().Set("Retry-After", "1")
			httpapi.WriteError(w, r, httpapi.Error(http.StatusServiceUnavailable, "common.draining").
				Public("서비스가 종료 준비 중입니다.").
				New("service is draining"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   h.service,
		"timestamp": h.now().Format(time.RFC3339),
	})
}

func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	status := "ready"
	statusCode := http.StatusOK
	results := make(map[string]string, len(h.checks)+1)
	if h.Draining() {
		status = "not_ready"
		statusCode = http.StatusServiceUnavailable
		results["drain"] = "draining"
	} else {
		results["drain"] = "ok"
	}

	names := make([]string, 0, len(h.checks))
	for name := range h.checks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		checkCtx, cancel := context.WithTimeout(r.Context(), h.timeout)
		err := h.checks[name](checkCtx)
		cancel()
		if err != nil {
			status = "not_ready"
			statusCode = http.StatusServiceUnavailable
			results[name] = "error"
			continue
		}
		results[name] = "ok"
	}
	if h.setReady != nil {
		h.setReady(statusCode == http.StatusOK)
	}
	writeJSON(w, statusCode, map[string]any{
		"status":    status,
		"service":   h.service,
		"checks":    results,
		"timestamp": h.now().Format(time.RFC3339),
	})
}

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	if h.metrics != nil {
		h.metrics.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", metricsContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "# HELP service_ready Service readiness state.\n")
	_, _ = fmt.Fprintf(w, "# TYPE service_ready gauge\n")
	_, _ = fmt.Fprintf(w, "service_ready{service=%q} 1\n", h.service)
	for _, collect := range h.metricCollectors {
		collect(w)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
