package app

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/coupon-service/internal/domain/coupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
)

type App struct {
	server *nethttp.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	var store coupon.Repository
	checks := map[string]operational.Check{}
	if cfg.DatabaseURL != "" {
		postgres, err := coupon.OpenPostgresRepository(ctx, cfg.Postgres)
		if err != nil {
			return App{}, err
		}
		store = postgres
		checks["database"] = postgres.Ping
	} else {
		store = coupon.NewMemoryRepository()
	}

	registry := metrics.NewRegistry()
	options := []coupon.Option{coupon.WithMetrics(registry)}
	if cfg.RedisGateEnabled == "true" {
		redisClient, pendingTTL, idempotencyTTL, err := buildRedisClient(cfg)
		if err != nil {
			return App{}, err
		}
		checks["redis"] = func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		}
		options = append(options, coupon.WithRedis(redisClient, pendingTTL, idempotencyTTL), coupon.WithGateFailureMode(cfg.RedisGateFailureMode))
	}

	mux := nethttp.NewServeMux()
	couponhttp.RegisterRoutes(mux, coupon.NewService(store, options...), registry, checks)
	handler := httpmiddleware.Stack(httpmiddleware.Config{
		ServiceName:  config.ServiceName,
		Metrics:      registry,
		RoutePattern: couponhttp.RoutePattern,
	}, mux)
	return App{server: httpserver.New(cfg.HTTPAddr, handler)}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}

func buildRedisClient(cfg config.Config) (*redis.Client, time.Duration, time.Duration, error) {
	if cfg.RedisURL == "" {
		return nil, 0, 0, fmt.Errorf("redis url is required")
	}
	options, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		options = &redis.Options{Addr: cfg.RedisURL}
	}
	client := redis.NewClient(options)
	pendingTTL, err := time.ParseDuration(cfg.RedisGatePendingTTL)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("parse COUPON_REDIS_GATE_PENDING_TTL: %w", err)
	}
	idempotencyTTL, err := time.ParseDuration(cfg.RedisGateIdempotencyTTL)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("parse COUPON_REDIS_GATE_IDEMPOTENCY_TTL: %w", err)
	}
	return client, pendingTTL, idempotencyTTL, nil
}
