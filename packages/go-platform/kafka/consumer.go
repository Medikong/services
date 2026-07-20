package kafka

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/samber/oops"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Handler func(context.Context, *kgo.Record) error

type ConsumerConfig struct {
	Client        *kgo.Client
	Handler       Handler
	CommitTimeout time.Duration
}

// RunConsumer processes one record at a time and commits only after Handler succeeds.
// The kgo client must use DisableAutoCommit and BlockRebalanceOnPoll.
func RunConsumer(ctx context.Context, config ConsumerConfig) error {
	if config.Client == nil {
		return oops.In("kafka_consumer").Code("kafka.client_required").New("kafka client is required")
	}
	if config.Handler == nil {
		return oops.In("kafka_consumer").Code("kafka.handler_required").New("kafka handler is required")
	}
	if config.CommitTimeout <= 0 {
		config.CommitTimeout = 5 * time.Second
	}
	defer config.Client.CloseAllowingRebalance()

	for {
		fetches := config.Client.PollRecords(ctx, 1)
		if ctx.Err() != nil {
			return nil
		}
		if fetchErrors := fetches.Errors(); len(fetchErrors) > 0 {
			return oops.
				In("kafka_consumer").
				Code("kafka.fetch_failed").
				With("topic", fetchErrors[0].Topic, "partition", fetchErrors[0].Partition).
				Wrap(fetchErrors[0].Err)
		}
		for _, record := range fetches.Records() {
			if err := handleRecord(ctx, config, record); err != nil {
				if ctx.Err() != nil && errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
			config.Client.AllowRebalance()
		}
	}
}

func handleRecord(ctx context.Context, config ConsumerConfig, record *kgo.Record) error {
	ctx = otel.GetTextMapPropagator().Extract(ctx, recordCarrier{record: record})
	ctx, span := otel.Tracer("github.com/Medikong/services/packages/go-platform/kafka").Start(
		ctx,
		"process "+record.Topic,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", record.Topic),
			attribute.Int("messaging.destination.partition.id", int(record.Partition)),
			attribute.Int64("messaging.kafka.offset", record.Offset),
		),
	)
	defer span.End()
	if err := config.Handler(ctx, record); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "handler failed")
		return oops.
			In("kafka_consumer").
			Code("kafka.handler_failed").
			With("topic", record.Topic, "partition", record.Partition, "offset", record.Offset).
			Wrap(err)
	}
	commitCtx, cancel := context.WithTimeout(ctx, config.CommitTimeout)
	err := config.Client.CommitRecords(commitCtx, record)
	cancel()
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return ctx.Err()
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "offset commit failed")
		return oops.
			In("kafka_consumer").
			Code("kafka.offset_commit_failed").
			With("topic", record.Topic, "partition", record.Partition, "offset", record.Offset).
			Wrap(err)
	}
	return nil
}

func StartProducerSpan(ctx context.Context, topic string) (context.Context, trace.Span) {
	return otel.Tracer("github.com/Medikong/services/packages/go-platform/kafka").Start(
		ctx,
		"publish "+topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation", "publish"),
			attribute.String("messaging.destination.name", topic),
		),
	)
}

func Inject(ctx context.Context, record *kgo.Record) {
	if record == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, recordCarrier{record: record})
}

type recordCarrier struct {
	record *kgo.Record
}

func (c recordCarrier) Get(key string) string {
	for i := len(c.record.Headers) - 1; i >= 0; i-- {
		if c.record.Headers[i].Key == key {
			return string(c.record.Headers[i].Value)
		}
	}
	return ""
}

func (c recordCarrier) Set(key string, value string) {
	for i := range c.record.Headers {
		if c.record.Headers[i].Key == key {
			c.record.Headers[i].Value = []byte(value)
			return
		}
	}
	c.record.Headers = append(c.record.Headers, kgo.RecordHeader{Key: key, Value: []byte(value)})
}

func (c recordCarrier) Keys() []string {
	keys := make([]string, 0, len(c.record.Headers))
	for _, header := range c.record.Headers {
		keys = append(keys, header.Key)
	}
	return keys
}

var _ propagation.TextMapCarrier = recordCarrier{}

func RecordID(record *kgo.Record) string {
	return record.Topic + ":" + strconv.FormatInt(int64(record.Partition), 10) + ":" + strconv.FormatInt(record.Offset, 10)
}
