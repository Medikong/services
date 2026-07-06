package httpmiddleware

import (
	"net/http"
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
	return "api"
}

func requestSeverity(statusCode int, duration time.Duration) string {
	if statusCode >= http.StatusInternalServerError {
		return "ERROR"
	}
	if statusCode >= http.StatusBadRequest || duration >= time.Second {
		return "WARN"
	}
	return "INFO"
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
