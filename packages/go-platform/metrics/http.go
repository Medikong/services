package metrics

import (
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var httpDurationBuckets = prometheus.DefBuckets

type ServiceIdentity struct {
	Name        string
	Version     string
	Environment string
}

func (i ServiceIdentity) Validate() error {
	for name, value := range map[string]string{
		"service_name":        i.Name,
		"service_version":     i.Version,
		"service_environment": i.Environment,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

// HTTP records the shared low-cardinality RED metrics used by every Go HTTP service.
// Request and trace identifiers intentionally never become metric labels.
type HTTP struct {
	identity ServiceIdentity
	active   *prometheus.GaugeVec
	duration *prometheus.HistogramVec
}

func NewHTTP(registerer prometheus.Registerer, identity ServiceIdentity) (*HTTP, error) {
	if registerer == nil {
		return nil, fmt.Errorf("prometheus registerer is required")
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	httpMetrics := &HTTP{
		identity: identity,
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_server_active_requests",
			Help: "Currently active HTTP server requests.",
		}, []string{
			"service_name",
			"service_version",
			"service_environment",
			"http_route",
			"http_route_kind",
			"http_request_method",
		}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "HTTP server request duration in seconds.",
			Buckets: httpDurationBuckets,
		}, []string{
			"service_name",
			"service_version",
			"service_environment",
			"http_route",
			"http_route_kind",
			"http_request_method",
			"http_response_status_code",
		}),
	}
	if err := registerer.Register(httpMetrics.active); err != nil {
		return nil, fmt.Errorf("register http active requests metric: %w", err)
	}
	if err := registerer.Register(httpMetrics.duration); err != nil {
		registerer.Unregister(httpMetrics.active)
		return nil, fmt.Errorf("register http request duration metric: %w", err)
	}
	return httpMetrics, nil
}

func (m *HTTP) Begin(method, route, routeKind string) {
	method = boundedHTTPMethod(method)
	m.active.WithLabelValues(
		m.identity.Name,
		m.identity.Version,
		m.identity.Environment,
		route,
		routeKind,
		method,
	).Inc()
}

func (m *HTTP) End(method, activeRoute, activeRouteKind, route, routeKind, statusCode string, duration time.Duration) {
	method = boundedHTTPMethod(method)
	m.active.WithLabelValues(
		m.identity.Name,
		m.identity.Version,
		m.identity.Environment,
		activeRoute,
		activeRouteKind,
		method,
	).Dec()
	m.duration.WithLabelValues(
		m.identity.Name,
		m.identity.Version,
		m.identity.Environment,
		route,
		routeKind,
		method,
		statusCode,
	).Observe(duration.Seconds())
}

func boundedHTTPMethod(method string) string {
	normalized := strings.ToUpper(strings.TrimSpace(method))
	switch normalized {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE":
		return normalized
	default:
		return "OTHER"
	}
}
