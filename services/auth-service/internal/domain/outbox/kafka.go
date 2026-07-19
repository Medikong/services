package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/samber/oops"
	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaPublisher struct {
	client  *kgo.Client
	topic   string
	timeout time.Duration
}

func NewKafkaPublisher(_ context.Context, brokers []string, topic string, timeout time.Duration) (*KafkaPublisher, error) {
	if len(brokers) == 0 || topic == "" || timeout <= 0 {
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
	return &KafkaPublisher{client: client, topic: topic, timeout: timeout}, nil
}

func (p *KafkaPublisher) Publish(ctx context.Context, event Event) error {
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
	result := p.client.ProduceSync(publishCtx, &kgo.Record{
		Topic: p.topic,
		Key:   []byte(event.ID.String()),
		Value: payload,
	})
	if err := result.FirstErr(); err != nil {
		return oops.In("auth_outbox_kafka").Code("publisher.publish_failed").Wrap(err)
	}
	return nil
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
