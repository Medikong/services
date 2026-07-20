package observability

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/samber/oops"
	"go.opentelemetry.io/otel"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	platformmetrics "github.com/Medikong/services/packages/go-platform/metrics"
	platformtelemetry "github.com/Medikong/services/packages/go-platform/telemetry"
)

type Metrics struct {
	registry               *prometheus.Registry
	service                string
	auditTotal             *prometheus.CounterVec
	sessionProjectionTotal *prometheus.CounterVec
	ready                  prometheus.Gauge
	http                   *platformmetrics.HTTP
	meterProvider          *sdkmetric.MeterProvider
}

func NewMetrics(service, version, environment string) (*Metrics, error) {
	metrics := &Metrics{
		service:  service,
		registry: prometheus.NewRegistry(),
		auditTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_audit_outbox_attempts_total",
			Help: "Audit outbox delivery outcomes.",
		}, []string{"service_name", "result"}),
		sessionProjectionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_session_projection_attempts_total",
			Help: "Session status projection delivery outcomes.",
		}, []string{"service_name", "result"}),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "service_ready",
			Help: "Service readiness state.",
			ConstLabels: prometheus.Labels{
				"service_name":        service,
				"service_version":     version,
				"service_environment": environment,
			},
		}),
	}
	for name, collector := range map[string]prometheus.Collector{
		"audit":              metrics.auditTotal,
		"go":                 collectors.NewGoCollector(),
		"process":            collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		"readiness":          metrics.ready,
		"session_projection": metrics.sessionProjectionTotal,
	} {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, oops.In("auth_metrics").Code("metrics.register_failed").With("collector", name).Wrap(err)
		}
	}
	httpMetrics, err := platformmetrics.NewHTTP(metrics.registry, platformmetrics.ServiceIdentity{
		Name: service, Version: version, Environment: environment,
	})
	if err != nil {
		return nil, oops.In("auth_metrics").Code("metrics.http_register_failed").Wrap(err)
	}
	metrics.http = httpMetrics
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(metrics.registry))
	if err != nil {
		return nil, oops.In("auth_metrics").Code("metrics.exporter_failed").Wrap(err)
	}
	resource, err := platformtelemetry.Resource(service)
	if err != nil {
		return nil, err
	}
	metrics.meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(resource),
	)
	otel.SetMeterProvider(metrics.meterProvider)
	metrics.ready.Set(0)
	return metrics, nil
}

func (m *Metrics) HTTP() *platformmetrics.HTTP {
	return m.http
}

func (m *Metrics) RecordAuditAttempt(result string) {
	m.auditTotal.WithLabelValues(m.service, result).Inc()
}

func (m *Metrics) RecordSessionProjectionAttempt(result string) {
	m.sessionProjectionTotal.WithLabelValues(m.service, result).Inc()
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (m *Metrics) SetReady(ready bool) {
	if ready {
		m.ready.Set(1)
		return
	}
	m.ready.Set(0)
}

func (m *Metrics) Shutdown(ctx context.Context) error {
	if m == nil || m.meterProvider == nil {
		return nil
	}
	return m.meterProvider.Shutdown(ctx)
}
