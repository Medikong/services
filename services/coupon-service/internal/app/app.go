package app

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/coupon-service/internal/domain/coupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
)

type App struct {
	server *nethttp.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	var store coupon.Repository
	if cfg.DatabaseURL != "" {
		postgres, err := coupon.OpenPostgresRepository(ctx, cfg.DatabaseURL)
		if err != nil {
			return App{}, err
		}
		store = postgres
	} else {
		store = coupon.NewMemoryRepository()
	}

	registry := metrics.NewRegistry()
	options := []coupon.Option{coupon.WithMetrics(registry)}
	if cfg.RedisGateEnabled == "true" {
		redisGate, err := buildRedisGate(cfg)
		if err != nil {
			return App{}, err
		}
		options = append(options, coupon.WithIssueGate(redisGate), coupon.WithGateFailureMode(cfg.RedisGateFailureMode))
	}

	mux := nethttp.NewServeMux()
	couponhttp.RegisterRoutes(mux, coupon.NewService(store, options...), registry)
	return App{server: httpserver.New(cfg.HTTPAddr, telemetry.Middleware(config.ServiceName, mux))}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}

func buildRedisGate(cfg config.Config) (*coupon.Redis, error) {
	client, err := coupon.NewRedisClient(cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	pendingTTL, err := time.ParseDuration(cfg.RedisGatePendingTTL)
	if err != nil {
		return nil, fmt.Errorf("parse COUPON_REDIS_GATE_PENDING_TTL: %w", err)
	}
	idempotencyTTL, err := time.ParseDuration(cfg.RedisGateIdempotencyTTL)
	if err != nil {
		return nil, fmt.Errorf("parse COUPON_REDIS_GATE_IDEMPOTENCY_TTL: %w", err)
	}
	return coupon.NewRedis(coupon.RedisConfig{
		Client:        client,
		PendingTTL:    pendingTTL,
		IdempotentTTL: idempotencyTTL,
	}), nil
}
