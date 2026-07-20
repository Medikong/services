//go:build integration

package integration_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/services/auth-service/internal/app"
	authmigration "github.com/Medikong/services/services/auth-service/internal/infrastructure/migration"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestRuntimeReadinessAndOutdatedSchemaRefusal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := startPostgres(t, ctx)
	redisURL := startRedis(t, ctx)
	configureDevelopmentEnvironment(t, databaseURL, redisURL)

	serverCfg, err := config.LoadServer()
	if err != nil {
		t.Fatalf("load server config: %v", err)
	}
	workerCfg, err := config.LoadWorker()
	if err != nil {
		t.Fatalf("load worker config: %v", err)
	}
	serverCfg.HTTP.PublicAddr = unusedAddress(t)
	serverCfg.HTTP.AdminAddr = unusedAddress(t)
	serverCfg.HTTP.DrainDelay = time.Millisecond
	serverCfg.Lifecycle.ShutdownTimeout = 2 * time.Second
	workerCfg.AdminAddr = unusedAddress(t)
	workerCfg.Lifecycle.ShutdownTimeout = 2 * time.Second

	db := migrateSchemas(t, ctx, serverCfg.Postgres)
	t.Cleanup(db.Close)

	testServerReadiness(t, serverCfg)
	testWorkerReadiness(t, workerCfg)

	if _, err := db.Exec(ctx, "DROP TABLE auth_dev_goose_db_version"); err != nil {
		t.Fatalf("drop development migration version: %v", err)
	}
	if _, err := app.NewServer(ctx, serverCfg, app.ServerOptions{}); err == nil {
		t.Fatal("NewServer() error = nil with an outdated development schema")
	}
	assertTableMissing(t, ctx, db, "auth_dev_goose_db_version")

	if _, err := db.Exec(ctx, "DROP TABLE auth_goose_db_version"); err != nil {
		t.Fatalf("drop core migration version: %v", err)
	}
	if _, err := app.NewWorker(ctx, workerCfg); err == nil {
		t.Fatal("NewWorker() error = nil with an outdated core schema")
	}
	assertTableMissing(t, ctx, db, "auth_goose_db_version")
}

func startPostgres(t *testing.T, ctx context.Context) string {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
	return databaseURL
}

func startRedis(t *testing.T, ctx context.Context) string {
	t.Helper()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "redis:7.4-alpine", ExposedPorts: []string{"6379/tcp"},
			WaitingFor: wait.ForLog("Ready to accept connections"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("redis host: %v", err)
	}
	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("redis port: %v", err)
	}
	return "redis://" + host + ":" + port.Port() + "/0"
}

func configureDevelopmentEnvironment(t *testing.T, databaseURL, redisURL string) {
	t.Helper()
	t.Setenv("SERVICE_ENVIRONMENT", "test")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("AUTH_SESSION_STATUS_ENABLED", "true")
	t.Setenv("REDIS_URL", redisURL)
	t.Setenv("AUTH_SESSION_STATUS_MAX_DB_LOOKUPS", "32")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "true")
	t.Setenv("AUTH_DEV_ROUTE_ENABLED", "true")
	t.Setenv("AUTH_VIRTUAL_ADAPTERS_ENABLED", "true")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", "integration-development-token")
	t.Setenv("AUTH_VIRTUAL_MESSAGE_KEY", "01234567890123456789012345678901")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", integrationJWTPrivateKeyPEM(t))
	t.Setenv("AUTH_JWT_KEY_ID", "integration-key")
	t.Setenv("AUTH_JWT_ISSUER", "integration")
	t.Setenv("AUTH_JWT_AUDIENCES", "dropmong-api")
}

func migrateSchemas(t *testing.T, ctx context.Context, postgres platformdb.PostgresConfig) *pgxpool.Pool {
	t.Helper()
	db := migrateProductionSchemas(t, ctx, postgres)
	if err := authmigration.MigrateDevelopment(ctx, db); err != nil {
		db.Close()
		t.Fatalf("migrate development auth schema: %v", err)
	}
	return db
}

func migrateProductionSchemas(t *testing.T, ctx context.Context, postgres platformdb.PostgresConfig) *pgxpool.Pool {
	t.Helper()
	db, err := platformdb.OpenPostgres(ctx, postgres)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := audit.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatalf("migrate audit schema: %v", err)
	}
	if err := authmigration.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatalf("migrate auth schema: %v", err)
	}
	return db
}

func testServerReadiness(t *testing.T, cfg config.ServerConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	server, err := app.NewServer(ctx, cfg, app.ServerOptions{})
	if err != nil {
		cancel()
		t.Fatalf("NewServer() error = %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- server.Run(ctx) }()
	waitReady(t, "http://"+cfg.HTTP.AdminAddr+"/readyz")
	cancel()
	assertStopped(t, result, "server")
}

func testWorkerReadiness(t *testing.T, cfg config.WorkerConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	worker, err := app.NewWorker(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("NewWorker() error = %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- worker.Run(ctx) }()
	waitReady(t, "http://"+cfg.AdminAddr+"/readyz")
	cancel()
	assertStopped(t, result, "worker")
}

func waitReady(t *testing.T, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("readiness did not become ready at %s: %v", endpoint, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertStopped(t *testing.T, result <-chan error, process string) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("%s stopped with error: %v", process, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not stop within shutdown timeout", process)
	}
}

func assertTableMissing(t *testing.T, ctx context.Context, db *pgxpool.Pool, table string) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if exists {
		t.Fatalf("runtime schema check recreated table %s", table)
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}
	return address
}
