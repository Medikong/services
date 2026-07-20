package httpmiddleware

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func routePattern(route func(*http.Request) string, r *http.Request) string {
	if route == nil {
		return "unmatched"
	}
	pattern := route(r)
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func routeKind(route string) string {
	switch route {
	case "/healthz", "/readyz", "/metrics":
		return "probe"
	}
	if route == "unmatched" {
		return "unmatched"
	}
	if strings.HasPrefix(route, "/debug") || strings.HasPrefix(route, "/_debug") || strings.HasPrefix(route, "/__debug") {
		return "debug"
	}
	return "api"
}

func requestSeverity(statusCode int, duration time.Duration) (string, slog.Level) {
	if statusCode >= http.StatusInternalServerError {
		return "ERROR", slog.LevelError
	}
	if statusCode >= http.StatusBadRequest || duration >= time.Second {
		return "WARN", slog.LevelWarn
	}
	return "INFO", slog.LevelInfo
}

func logPolicy(routeKind string, statusCode int, duration time.Duration) string {
	if routeKind == "probe" && statusCode < http.StatusInternalServerError {
		return "drop"
	}
	if statusCode >= http.StatusInternalServerError || duration >= time.Second {
		return "keep"
	}
	if statusCode >= http.StatusBadRequest || routeKind == "debug" {
		return "keep"
	}
	return "sample"
}
