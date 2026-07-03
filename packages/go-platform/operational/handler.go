package operational

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

type Check func(ctx context.Context) error

type Handler struct {
	service string
	checks  map[string]Check
	now     func() time.Time
}

func New(service string, checks map[string]Check) Handler {
	if checks == nil {
		checks = map[string]Check{}
	}
	return Handler{
		service: service,
		checks:  checks,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)
	mux.HandleFunc("GET /metrics", h.Metrics)
}

func (h Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   h.service,
		"timestamp": h.now().Format(time.RFC3339),
	})
}

func (h Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	status := "ready"
	code := http.StatusOK
	checks := make(map[string]string, len(h.checks))

	names := make([]string, 0, len(h.checks))
	for name := range h.checks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := h.checks[name](r.Context()); err != nil {
			status = "not_ready"
			code = http.StatusServiceUnavailable
			checks[name] = "error"
			continue
		}
		checks[name] = "ok"
	}

	writeJSON(w, code, map[string]any{
		"status":    status,
		"service":   h.service,
		"checks":    checks,
		"timestamp": h.now().Format(time.RFC3339),
	})
}

func (h Handler) Metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", metricsContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "# HELP service_ready Service readiness state.\n")
	_, _ = fmt.Fprintf(w, "# TYPE service_ready gauge\n")
	_, _ = fmt.Fprintf(w, "service_ready{service=%q} 1\n", h.service)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
