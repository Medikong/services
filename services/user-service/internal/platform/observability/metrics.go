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
	service       string
	registry      *prometheus.Registry
	operations    *prometheus.CounterVec
	ready         prometheus.Gauge
	meterProvider *sdkmetric.MeterProvider
}

func NewMetrics(service string) (*Metrics, error) {
	metrics := &Metrics{
		service:  service,
		registry: prometheus.NewRegistry(),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "user_operations_total",
			Help: "User domain operation outcomes.",
		}, []string{"service_name", "operation", "result"}),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "service_ready",
			Help:        "Service readiness state.",
			ConstLabels: prometheus.Labels{"service_name": service},
		}),
	}
	for name, collector := range map[string]prometheus.Collector{
		"go":        collectors.NewGoCollector(),
		"process":   collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		"operation": metrics.operations,
		"readiness": metrics.ready,
	} {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, oops.In("user_metrics").Code("metrics.register_failed").With("collector", name).Wrap(err)
		}
	}
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(metrics.registry))
	if err != nil {
		return nil, oops.In("user_metrics").Code("metrics.exporter_failed").Wrap(err)
	}
	resource, err := platformtelemetry.Resource(service)
	if err != nil {
		return nil, err
	}
	metrics.meterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithResource(resource))
	otel.SetMeterProvider(metrics.meterProvider)
	metrics.ready.Set(0)
	return metrics, nil
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (m *Metrics) RecordOperation(operation, result string) {
	m.operations.WithLabelValues(m.service, operation, result).Inc()
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
