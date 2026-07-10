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

	platformtelemetry "github.com/Medikong/services/packages/go-platform/telemetry"
)

type Metrics struct {
	service        string
	registry       *prometheus.Registry
	lockTotal      *prometheus.CounterVec
	operationTotal *prometheus.CounterVec
	auditTotal     *prometheus.CounterVec
	cleanupTotal   *prometheus.CounterVec
	cleanedRows    prometheus.Counter
	ready          prometheus.Gauge
	meterProvider  *sdkmetric.MeterProvider
}

func NewMetrics(service string) (*Metrics, error) {
	metrics := &Metrics{
		service:  service,
		registry: prometheus.NewRegistry(),
		lockTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "distributed_lock_operations_total",
			Help: "Redis distributed lock outcomes.",
		}, []string{"service_name", "result"}),
		operationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reference_operations_total",
			Help: "Reference endpoint outcomes. Replace this metric with a domain metric.",
		}, []string{"service_name", "result"}),
		auditTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audit_outbox_attempts_total",
			Help: "Audit outbox delivery outcomes.",
		}, []string{"service_name", "result"}),
		cleanupTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audit_outbox_cleanup_runs_total",
			Help: "Audit outbox cleanup outcomes.",
		}, []string{"service_name", "result"}),
		cleanedRows: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "audit_outbox_cleaned_rows_total",
			Help:        "Delivered audit outbox rows deleted by retention cleanup.",
			ConstLabels: prometheus.Labels{"service_name": service},
		}),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "service_ready",
			Help:        "Service readiness state.",
			ConstLabels: prometheus.Labels{"service_name": service},
		}),
	}
	if err := metrics.registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, registerError("go", err)
	}
	if err := metrics.registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, registerError("process", err)
	}
	for name, collector := range map[string]prometheus.Collector{
		"lock":      metrics.lockTotal,
		"operation": metrics.operationTotal,
		"audit":     metrics.auditTotal,
		"cleanup":   metrics.cleanupTotal,
		"cleaned":   metrics.cleanedRows,
		"readiness": metrics.ready,
	} {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, registerError(name, err)
		}
	}
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(metrics.registry))
	if err != nil {
		return nil, registerError("opentelemetry", err)
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

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (m *Metrics) RecordLock(result string) {
	m.lockTotal.WithLabelValues(m.service, result).Inc()
}

func (m *Metrics) RecordOperation(result string) {
	m.operationTotal.WithLabelValues(m.service, result).Inc()
}

func (m *Metrics) RecordAuditAttempt(result string) {
	m.auditTotal.WithLabelValues(m.service, result).Inc()
}

func (m *Metrics) RecordAuditCleanup(result string, deleted int64) {
	m.cleanupTotal.WithLabelValues(m.service, result).Inc()
	if deleted > 0 {
		m.cleanedRows.Add(float64(deleted))
	}
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

func registerError(name string, err error) error {
	return oops.
		In("reference_metrics").
		Code("metrics.register_failed").
		With("collector", name).
		Wrapf(err, "register metric collector %s", name)
}
