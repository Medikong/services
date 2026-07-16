package redisutil

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var configErr = oops.In("platform_redis_config").Code("config.invalid")

type Option func(*clientOptions) error

type clientOptions struct {
	tracing []redisotel.TracingOption
	metrics []redisotel.MetricsOption
}

func WithTracerProvider(provider trace.TracerProvider) Option {
	return func(options *clientOptions) error {
		if provider == nil {
			return configErr.With("setting", "tracer_provider").New("tracer provider is required")
		}
		options.tracing = append(options.tracing, redisotel.WithTracerProvider(provider))
		return nil
	}
}

func WithMeterProvider(provider metric.MeterProvider) Option {
	return func(options *clientOptions) error {
		if provider == nil {
			return configErr.With("setting", "meter_provider").New("meter provider is required")
		}
		options.metrics = append(options.metrics, redisotel.WithMeterProvider(provider))
		return nil
	}
}

func WithTracingOptions(tracingOptions ...redisotel.TracingOption) Option {
	return func(options *clientOptions) error {
		for _, option := range tracingOptions {
			if option == nil {
				return configErr.With("setting", "tracing_option").New("tracing option is required")
			}
		}
		options.tracing = append(options.tracing, tracingOptions...)
		return nil
	}
}

func WithMetricsOptions(metricsOptions ...redisotel.MetricsOption) Option {
	return func(options *clientOptions) error {
		for _, option := range metricsOptions {
			if option == nil {
				return configErr.With("setting", "metrics_option").New("metrics option is required")
			}
		}
		options.metrics = append(options.metrics, metricsOptions...)
		return nil
	}
}

type Config struct {
	URL          string
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

func LoadConfigFromEnv(redisURL string) (Config, error) {
	poolSize, err := intEnv("REDIS_POOL_SIZE", 10)
	if err != nil {
		return Config{}, err
	}
	minIdleConns, err := intEnv("REDIS_MIN_IDLE_CONNS", 1)
	if err != nil {
		return Config{}, err
	}
	dialTimeout, err := durationEnv("REDIS_DIAL_TIMEOUT", 3*time.Second)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := durationEnv("REDIS_READ_TIMEOUT", time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := durationEnv("REDIS_WRITE_TIMEOUT", time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		URL:          strings.TrimSpace(redisURL),
		PoolSize:     poolSize,
		MinIdleConns: minIdleConns,
		DialTimeout:  dialTimeout,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.URL, validation.Required),
		validation.Field(&c.PoolSize, validation.Min(1)),
		validation.Field(&c.MinIdleConns, validation.Min(0), validation.Max(c.PoolSize)),
		validation.Field(&c.DialTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.ReadTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.WriteTimeout, validation.Min(time.Nanosecond)),
	)
	if err != nil {
		return configErr.With("config", "redis").Wrap(err)
	}
	return nil
}

func Open(ctx context.Context, cfg Config, optionFns ...Option) (*goredis.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	clientConfig, err := applyOptions(optionFns)
	if err != nil {
		return nil, err
	}
	options, err := goredis.ParseURL(cfg.URL)
	if err != nil {
		return nil, oops.In("platform_redis").Code("redis.invalid_url").Wrap(err)
	}
	options.PoolSize = cfg.PoolSize
	options.MinIdleConns = cfg.MinIdleConns
	options.DialTimeout = cfg.DialTimeout
	options.ReadTimeout = cfg.ReadTimeout
	options.WriteTimeout = cfg.WriteTimeout

	client := goredis.NewClient(options)
	if err := prepareClient(ctx, client, clientConfig); err != nil {
		return nil, err
	}
	return client, nil
}

func OpenCluster(
	ctx context.Context,
	options *goredis.ClusterOptions,
	optionFns ...Option,
) (*goredis.ClusterClient, error) {
	if options == nil {
		return nil, configErr.With("config", "redis_cluster").New("cluster options are required")
	}
	clientConfig, err := applyOptions(optionFns)
	if err != nil {
		return nil, err
	}
	client := goredis.NewClusterClient(options)
	if err := prepareClient(ctx, client, clientConfig); err != nil {
		return nil, err
	}
	return client, nil
}

func OpenFailover(
	ctx context.Context,
	options *goredis.FailoverOptions,
	optionFns ...Option,
) (*goredis.Client, error) {
	if options == nil {
		return nil, configErr.With("config", "redis_failover").New("failover options are required")
	}
	clientConfig, err := applyOptions(optionFns)
	if err != nil {
		return nil, err
	}
	client := goredis.NewFailoverClient(options)
	if err := prepareClient(ctx, client, clientConfig); err != nil {
		return nil, err
	}
	return client, nil
}

func applyOptions(optionFns []Option) (clientOptions, error) {
	options := clientOptions{}
	for _, option := range optionFns {
		if option == nil {
			return clientOptions{}, configErr.With("setting", "client_option").New("client option is required")
		}
		if err := option(&options); err != nil {
			return clientOptions{}, err
		}
	}
	return options, nil
}

func prepareClient(ctx context.Context, client goredis.UniversalClient, options clientOptions) error {
	tracingOptions := append(
		append([]redisotel.TracingOption{}, options.tracing...),
		redisotel.WithDBStatement(false),
	)
	if err := redisotel.InstrumentTracing(client, tracingOptions...); err != nil {
		_ = client.Close()
		return oops.In("platform_redis").Code("redis.trace_instrumentation_failed").Wrap(err)
	}
	if err := redisotel.InstrumentMetrics(client, options.metrics...); err != nil {
		_ = client.Close()
		return oops.In("platform_redis").Code("redis.metric_instrumentation_failed").Wrap(err)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return oops.In("platform_redis").Code("redis.ping_failed").Wrap(err)
	}
	return nil
}

func intEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, configErr.With("setting", name, "value", raw).Wrap(err)
	}
	return value, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, configErr.With("setting", name, "value", raw).Wrap(err)
	}
	return value, nil
}
