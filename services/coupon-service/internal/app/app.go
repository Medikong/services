package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/coupon-service/internal/config"
	"github.com/Medikong/services/services/coupon-service/internal/gate"
	"github.com/Medikong/services/services/coupon-service/internal/handler"
	"github.com/Medikong/services/services/coupon-service/internal/service"
	"github.com/Medikong/services/services/coupon-service/internal/store/memory"
	postgresstore "github.com/Medikong/services/services/coupon-service/internal/store/postgres"
)

type App struct {
	server *http.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	var store service.Store
	if cfg.DatabaseURL != "" {
		postgres, err := postgresstore.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return App{}, err
		}
		store = postgres
	} else {
		store = memory.New()
	}

	registry := metrics.NewRegistry()
	options := []service.Option{service.WithMetrics(registry)}
	if cfg.RedisGateEnabled == "true" {
		redisGate, err := buildRedisGate(cfg)
		if err != nil {
			return App{}, err
		}
		options = append(options, service.WithIssueGate(redisGate), service.WithGateFailureMode(cfg.RedisGateFailureMode))
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, service.New(store, options...), registry)
	return App{server: httpserver.New(cfg.HTTPAddr, telemetry.Middleware(config.ServiceName, mux))}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}

func buildRedisGate(cfg config.Config) (*gate.Redis, error) {
	client, err := gate.NewRedisClient(cfg.RedisURL)
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
	return gate.NewRedis(gate.RedisConfig{
		Client:        client,
		PendingTTL:    pendingTTL,
		IdempotentTTL: idempotencyTTL,
	}), nil
}
