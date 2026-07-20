package redisutil

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("REDIS_POOL_SIZE", "20")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "2")
	t.Setenv("REDIS_DIAL_TIMEOUT", "5s")

	cfg, err := LoadConfigFromEnv("redis://localhost:6379/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.PoolSize != 20 || cfg.MinIdleConns != 2 || cfg.DialTimeout != 5*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoadConfigFromEnvRejectsInvalidPoolRange(t *testing.T) {
	t.Setenv("REDIS_POOL_SIZE", "1")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "2")

	if _, err := LoadConfigFromEnv("redis://localhost:6379/0"); err == nil {
		t.Fatal("LoadConfigFromEnv() error = nil, want invalid pool range")
	}
}

func TestOpenReturnsRawClient(t *testing.T) {
	server := miniredis.RunT(t)
	cfg, err := LoadConfigFromEnv("redis://" + server.Addr() + "/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	client, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Set(context.Background(), "key", "value", 0).Err(); err != nil {
		t.Fatalf("raw redis client Set() error = %v", err)
	}
}

func TestOpenDoesNotExposeInvalidURLCredentials(t *testing.T) {
	const credential = "do-not-log-this-password"
	cfg, err := LoadConfigFromEnv("redis://worker:" + credential + "@%zz:6379/0")
	if err != nil {
		t.Fatal("LoadConfigFromEnv() unexpectedly rejected the URL before Open")
	}

	_, err = Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("Open() error = nil, want invalid URL error")
	}
	if strings.Contains(err.Error(), credential) || strings.Contains(err.Error(), cfg.URL) {
		t.Fatal("Open() error exposes Redis URL credentials")
	}
}

func TestOpenUsesInjectedProvidersAndSanitizesCommandArguments(t *testing.T) {
	server := miniredis.RunT(t)
	cfg, err := LoadConfigFromEnv("redis://" + server.Addr() + "/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	client, err := Open(
		context.Background(),
		cfg,
		WithTracerProvider(provider),
		WithMeterProvider(noop.NewMeterProvider()),
		WithTracingOptions(
			redisotel.WithCallerEnabled(false),
			redisotel.WithDBStatement(true),
		),
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, parent := provider.Tracer("redisutil-test").Start(context.Background(), "request")
	if err := client.Set(ctx, "secret:key", "secret-value", 0).Err(); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, err := client.Pipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.Set(ctx, "pipeline:key", "pipeline-secret", 0)
		pipe.Get(ctx, "pipeline:key")
		return nil
	}); err != nil {
		t.Fatalf("Pipelined() error = %v", err)
	}
	if err := client.EvalSha(
		ctx,
		"secret-sha",
		[]string{"lua:key"},
		"lua-secret",
	).Err(); err == nil {
		t.Fatal("EvalSha() error = nil, want missing script error")
	}
	parent.End()

	var commandSpanFound bool
	var luaErrorSpanFound bool
	for _, span := range exporter.GetSpans() {
		if span.SpanKind != trace.SpanKindClient {
			continue
		}
		for _, attribute := range span.Attributes {
			if attribute.Key == "db.statement" || attribute.Key == "db.query.text" {
				t.Fatalf("span %q contains command text attribute %q", span.Name, attribute.Key)
			}
			value := attribute.Value.Emit()
			for _, secret := range []string{
				"secret:key",
				"secret-value",
				"pipeline:key",
				"pipeline-secret",
				"secret-sha",
				"lua:key",
				"lua-secret",
			} {
				if strings.Contains(value, secret) {
					t.Fatalf("span %q attribute %q exposes %q", span.Name, attribute.Key, secret)
				}
			}
		}
		if strings.Contains(strings.ToLower(span.Name), "set") {
			commandSpanFound = true
			if span.Parent.SpanID() != parent.SpanContext().SpanID() {
				t.Fatalf("span %q parent = %s, want %s", span.Name, span.Parent.SpanID(), parent.SpanContext().SpanID())
			}
		}
		if strings.Contains(strings.ToLower(span.Name), "evalsha") {
			luaErrorSpanFound = true
			if span.Status.Code != codes.Error {
				t.Fatalf("span %q status = %v, want error", span.Name, span.Status.Code)
			}
			if span.Parent.SpanID() != parent.SpanContext().SpanID() {
				t.Fatalf("span %q parent = %s, want %s", span.Name, span.Parent.SpanID(), parent.SpanContext().SpanID())
			}
		}
	}
	if !commandSpanFound {
		t.Fatal("Redis SET client span not found")
	}
	if !luaErrorSpanFound {
		t.Fatal("Redis EVALSHA error span not found")
	}
}

func TestOpenRejectsInvalidOptions(t *testing.T) {
	server := miniredis.RunT(t)
	cfg, err := LoadConfigFromEnv("redis://" + server.Addr() + "/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	tests := []struct {
		name   string
		option Option
	}{
		{name: "nil option"},
		{name: "nil tracer provider", option: WithTracerProvider(nil)},
		{name: "nil meter provider", option: WithMeterProvider(nil)},
		{name: "nil tracing option", option: WithTracingOptions(nil)},
		{name: "nil metrics option", option: WithMetricsOptions(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Open(context.Background(), cfg, tt.option); err == nil {
				t.Fatal("Open() error = nil, want invalid option")
			}
		})
	}
}

func TestOpenClusterAndFailoverValidateAndCloseFailedClients(t *testing.T) {
	if _, err := OpenCluster(context.Background(), nil); err == nil {
		t.Fatal("OpenCluster() error = nil, want nil options error")
	}
	cluster, err := OpenCluster(context.Background(), &goredis.ClusterOptions{
		Addrs:        []string{"127.0.0.1:1"},
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		MaxRetries:   0,
	})
	if err == nil || cluster != nil {
		t.Fatalf("OpenCluster() = (%v, %v), want (nil, error)", cluster, err)
	}

	if _, err := OpenFailover(context.Background(), nil); err == nil {
		t.Fatal("OpenFailover() error = nil, want nil options error")
	}
	failover, err := OpenFailover(context.Background(), &goredis.FailoverOptions{
		MasterName:    "primary",
		SentinelAddrs: []string{"127.0.0.1:1"},
		DialTimeout:   10 * time.Millisecond,
		ReadTimeout:   10 * time.Millisecond,
		WriteTimeout:  10 * time.Millisecond,
		MaxRetries:    0,
	})
	if err == nil || failover != nil {
		t.Fatalf("OpenFailover() = (%v, %v), want (nil, error)", failover, err)
	}
}

func TestOpenAcceptsNoopTracerProvider(t *testing.T) {
	server := miniredis.RunT(t)
	cfg, err := LoadConfigFromEnv("redis://" + server.Addr() + "/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	client, err := Open(context.Background(), cfg, WithTracerProvider(tracenoop.NewTracerProvider()))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
}

func TestKeyBuilderBuildsSharedContract(t *testing.T) {
	builder, err := NewKeyBuilder("prod", "coupon-service", 1)
	if err != nil {
		t.Fatalf("NewKeyBuilder() error = %v", err)
	}

	key, err := builder.Build("campaign:admission", "user:1 / east")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if key != "prod:coupon-service:v1:campaign%3Aadmission:user%3A1%20%2F%20east" {
		t.Fatalf("Build() = %q", key)
	}

	builder, err = NewKeyBuilder("prod", "coupon-service", 2)
	if err != nil {
		t.Fatalf("NewKeyBuilder() error = %v", err)
	}
	key, err = builder.BuildWithHashTag("campaign:123", "campaign-admission", "user/9")
	if err != nil {
		t.Fatalf("BuildWithHashTag() error = %v", err)
	}
	if key != "prod:coupon-service:v2:{campaign%3A123}:campaign-admission:user%2F9" {
		t.Fatalf("BuildWithHashTag() = %q", key)
	}
}

func TestKeyBuilderRejectsInvalidKeys(t *testing.T) {
	if _, err := NewKeyBuilder("", "coupon-service", 1); err == nil {
		t.Fatal("NewKeyBuilder() error = nil, want empty environment error")
	}
	if _, err := NewKeyBuilder("prod", "coupon:service", 1); err == nil {
		t.Fatal("NewKeyBuilder() error = nil, want invalid service error")
	}
	if _, err := NewKeyBuilder("prod", "coupon-service", 0); err == nil {
		t.Fatal("NewKeyBuilder() error = nil, want invalid schema version error")
	}
	builder, err := NewKeyBuilder("prod", "coupon-service", 1)
	if err != nil {
		t.Fatalf("NewKeyBuilder() error = %v", err)
	}

	tests := []struct {
		name  string
		build func() (string, error)
	}{
		{name: "missing identifier", build: func() (string, error) { return builder.Build() }},
		{name: "empty identifier", build: func() (string, error) { return builder.Build("") }},
		{name: "empty hash tag", build: func() (string, error) { return builder.BuildWithHashTag("", "id") }},
		{name: "key too long", build: func() (string, error) { return builder.Build(strings.Repeat("x", MaxKeyLength)) }},
		{name: "zero builder", build: func() (string, error) { return (KeyBuilder{}).Build("id") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.build(); err == nil {
				t.Fatal("build error = nil, want invalid key error")
			}
		})
	}
}
