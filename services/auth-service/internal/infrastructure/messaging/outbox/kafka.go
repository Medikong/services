package outbox

import (
	"context"
	"encoding/json"
	"time"

	platformkafka "github.com/Medikong/services/packages/go-platform/kafka"
	"github.com/Medikong/services/packages/go-platform/logger"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/samber/oops"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type KafkaPublisher struct {
	client      *kgo.Client
	service     string
	version     string
	environment string
	topic       string
	timeout     time.Duration
}

func NewKafkaPublisher(
	_ context.Context,
	service string,
	version string,
	environment string,
	brokers []string,
	topic string,
	timeout time.Duration,
) (*KafkaPublisher, error) {
	if service == "" || version == "" || environment == "" || len(brokers) == 0 || topic == "" || timeout <= 0 {
		return nil, oops.In("auth_outbox_kafka").Code("publisher.invalid_config").New("invalid auth outbox Kafka configuration")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return nil, oops.In("auth_outbox_kafka").Code("publisher.open_failed").Wrap(err)
	}
	return &KafkaPublisher{
		client:      client,
		service:     service,
		version:     version,
		environment: environment,
		topic:       topic,
		timeout:     timeout,
	}, nil
}

func (p *KafkaPublisher) Publish(ctx context.Context, event domainoutbox.Event) error {
	payload, err := json.Marshal(struct {
		EventID       string          `json:"eventId"`
		EventType     string          `json:"eventType"`
		AggregateType string          `json:"aggregateType"`
		AggregateID   string          `json:"aggregateId"`
		AggregateVer  int64           `json:"aggregateVersion"`
		CorrelationID string          `json:"correlationId"`
		OccurredAt    time.Time       `json:"occurredAt"`
		Payload       json.RawMessage `json:"payload"`
	}{
		EventID: event.ID.String(), EventType: event.Type, AggregateType: event.AggregateType,
		AggregateID: event.AggregateID.String(), AggregateVer: event.Version,
		CorrelationID: event.CorrelationID.String(), OccurredAt: event.OccurredAt.UTC(), Payload: event.Payload,
	})
	if err != nil {
		return oops.In("auth_outbox_kafka").Code("publisher.encode_failed").Wrap(err)
	}
	publishCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	publishCtx, span := platformkafka.StartProducerSpan(publishCtx, p.topic)
	span.SetAttributes(
		attribute.String("messaging.message.type", event.Type),
		attribute.String("messaging.message.conversation_id", event.CorrelationID.String()),
	)
	defer span.End()
	record := &kgo.Record{
		Topic: p.topic,
		Key:   []byte(event.ID.String()),
		Value: payload,
	}
	platformkafka.Inject(publishCtx, record)
	result := p.client.ProduceSync(publishCtx, record)
	if err := result.FirstErr(); err != nil {
		safeErr := oops.In("auth_outbox_kafka").Code("publisher.publish_failed").New("Kafka publish failed")
		span.RecordError(safeErr)
		span.SetStatus(codes.Error, "publish failed")
		p.logPublish(publishCtx, event, "failure")
		return oops.In("auth_outbox_kafka").Code("publisher.publish_failed").Wrap(err)
	}
	p.logPublish(publishCtx, event, "success")
	return nil
}

func (p *KafkaPublisher) logPublish(ctx context.Context, event domainoutbox.Event, outcome string) {
	traceID, spanID := logger.TraceIDs(ctx)
	log := logger.Info
	if outcome == "failure" {
		log = logger.Error
	}
	log(ctx, "kafka.message.publish",
		"event", "kafka.message.publish",
		"service.name", p.service,
		"service.version", p.version,
		"service.environment", p.environment,
		"messaging.system", "kafka",
		"messaging.operation", "publish",
		"messaging.destination.name", p.topic,
		"messaging.message.type", event.Type,
		"correlation_id", event.CorrelationID.String(),
		"trace_id", traceID,
		"span_id", spanID,
		"outcome", outcome,
		"log.kind", "messaging",
		"log.policy", "keep",
	)
}

func (p *KafkaPublisher) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.client.Ping(pingCtx)
}

func (p *KafkaPublisher) Close() {
	if p != nil && p.client != nil {
		p.client.Close()
	}
}
