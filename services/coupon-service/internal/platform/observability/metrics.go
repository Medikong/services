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
	service         string
	registry        *prometheus.Registry
	operationTotal  *prometheus.CounterVec
	workerTotal     *prometheus.CounterVec
	outboxTotal     *prometheus.CounterVec
	inboxTotal      *prometheus.CounterVec
	redisGateTotal  *prometheus.CounterVec
	dbFinalizeTotal *prometheus.CounterVec
	ready           prometheus.Gauge
	meterProvider   *sdkmetric.MeterProvider
}

func NewMetrics(service string) (*Metrics, error) {
	metrics := &Metrics{
		service:  service,
		registry: prometheus.NewRegistry(),
		operationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_operations_total",
			Help: "Coupon application operation outcomes.",
		}, []string{"service_name", "operation", "result"}),
		workerTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_worker_attempts_total",
			Help: "Coupon worker attempt outcomes.",
		}, []string{"service_name", "worker", "result"}),
		outboxTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_outbox_publish_total",
			Help: "Coupon domain outbox delivery outcomes.",
		}, []string{"service_name", "event_type", "result"}),
		inboxTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_inbox_process_total",
			Help: "Coupon inbox processing outcomes.",
		}, []string{"service_name", "message_type", "result"}),
		redisGateTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_redis_gate_total",
			Help: "Coupon Redis admission gate outcomes.",
		}, []string{"service_name", "result"}),
		dbFinalizeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_db_finalize_total",
			Help: "Coupon PostgreSQL finalization outcomes after admission.",
		}, []string{"service_name", "result"}),
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
		"operation":   metrics.operationTotal,
		"worker":      metrics.workerTotal,
		"outbox":      metrics.outboxTotal,
		"inbox":       metrics.inboxTotal,
		"redis_gate":  metrics.redisGateTotal,
		"db_finalize": metrics.dbFinalizeTotal,
		"readiness":   metrics.ready,
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

func (m *Metrics) RecordOperation(operation, result string) {
	m.operationTotal.WithLabelValues(m.service, operation, result).Inc()
}

func (m *Metrics) RecordWorker(worker, result string) {
	m.workerTotal.WithLabelValues(m.service, worker, result).Inc()
}

func (m *Metrics) RecordOutbox(eventType, result string) {
	m.outboxTotal.WithLabelValues(m.service, eventType, result).Inc()
}

func (m *Metrics) RecordInbox(messageType, result string) {
	m.inboxTotal.WithLabelValues(m.service, messageType, result).Inc()
}

func (m *Metrics) RecordRedisGate(result string) {
	m.redisGateTotal.WithLabelValues(m.service, result).Inc()
}

func (m *Metrics) RecordDBFinalize(result string) {
	m.dbFinalizeTotal.WithLabelValues(m.service, result).Inc()
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
		In("coupon_metrics").
		Code("metrics.register_failed").
		With("collector", name).
		Wrapf(err, "register metric collector %s", name)
}
