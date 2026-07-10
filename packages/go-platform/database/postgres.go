package database

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresConfig struct {
	DatabaseURL       string
	MaxConns          int32
	MinConns          int32
	MinIdleConns      int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

func DefaultPostgresConfig(databaseURL string) PostgresConfig {
	return PostgresConfig{
		DatabaseURL:       databaseURL,
		MaxConns:          10,
		MinConns:          0,
		MinIdleConns:      0,
		MaxConnLifetime:   30 * time.Minute,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
	}
}

func LoadPostgresConfigFromEnv(databaseURL string) (PostgresConfig, error) {
	cfg := DefaultPostgresConfig(databaseURL)
	var err error
	if cfg.MaxConns, err = envInt32("POSTGRES_POOL_MAX_CONNS", cfg.MaxConns); err != nil {
		return PostgresConfig{}, err
	}
	if cfg.MinConns, err = envInt32("POSTGRES_POOL_MIN_CONNS", cfg.MinConns); err != nil {
		return PostgresConfig{}, err
	}
	if cfg.MinIdleConns, err = envInt32("POSTGRES_POOL_MIN_IDLE_CONNS", cfg.MinIdleConns); err != nil {
		return PostgresConfig{}, err
	}
	if cfg.MaxConnLifetime, err = envDuration("POSTGRES_POOL_MAX_CONN_LIFETIME", cfg.MaxConnLifetime); err != nil {
		return PostgresConfig{}, err
	}
	if cfg.MaxConnIdleTime, err = envDuration("POSTGRES_POOL_MAX_CONN_IDLE_TIME", cfg.MaxConnIdleTime); err != nil {
		return PostgresConfig{}, err
	}
	if cfg.HealthCheckPeriod, err = envDuration("POSTGRES_POOL_HEALTH_CHECK_PERIOD", cfg.HealthCheckPeriod); err != nil {
		return PostgresConfig{}, err
	}
	if err := validatePostgresConfig(cfg); err != nil {
		return PostgresConfig{}, err
	}
	return cfg, nil
}

func OpenPostgres(ctx context.Context, config PostgresConfig) (*pgxpool.Pool, error) {
	if strings.TrimSpace(config.DatabaseURL) == "" {
		return nil, fmt.Errorf("database url is required")
	}
	if err := validatePostgresConfig(config); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(normalizeDatabaseURL(config.DatabaseURL))
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = config.MaxConns
	cfg.MinConns = config.MinConns
	cfg.MinIdleConns = config.MinIdleConns
	cfg.MaxConnLifetime = config.MaxConnLifetime
	cfg.MaxConnIdleTime = config.MaxConnIdleTime
	cfg.HealthCheckPeriod = config.HealthCheckPeriod
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := otelpgx.RecordStats(pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func RunMigrations(ctx context.Context, pool *pgxpool.Pool, statements []string) (err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('dropmong:migrations'))`); err != nil {
		return err
	}
	defer func() {
		if _, unlockErr := conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('dropmong:migrations'))`); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	for _, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := conn.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func normalizeDatabaseURL(databaseURL string) string {
	return strings.Replace(databaseURL, "postgresql+psycopg://", "postgres://", 1)
}

func envInt32(name string, fallback int32) (int32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s as int32: %w", name, err)
	}
	return int32(parsed), nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s as duration: %w", name, err)
	}
	return parsed, nil
}

func validatePostgresConfig(config PostgresConfig) error {
	if config.MaxConns <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_CONNS must be greater than 0")
	}
	if config.MinConns < 0 {
		return fmt.Errorf("POSTGRES_POOL_MIN_CONNS must be greater than or equal to 0")
	}
	if config.MinIdleConns < 0 {
		return fmt.Errorf("POSTGRES_POOL_MIN_IDLE_CONNS must be greater than or equal to 0")
	}
	if config.MinConns > config.MaxConns {
		return fmt.Errorf("POSTGRES_POOL_MIN_CONNS must be less than or equal to POSTGRES_POOL_MAX_CONNS")
	}
	if config.MinIdleConns > config.MaxConns {
		return fmt.Errorf("POSTGRES_POOL_MIN_IDLE_CONNS must be less than or equal to POSTGRES_POOL_MAX_CONNS")
	}
	if config.MaxConnLifetime <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_CONN_LIFETIME must be greater than 0")
	}
	if config.MaxConnIdleTime <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_CONN_IDLE_TIME must be greater than 0")
	}
	if config.HealthCheckPeriod <= 0 {
		return fmt.Errorf("POSTGRES_POOL_HEALTH_CHECK_PERIOD must be greater than 0")
	}
	return nil
}
