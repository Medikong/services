//go:build integration

package integration_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/services/coupon-service/internal/app"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestServerAndWorkerLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	databaseURL := startPostgres(t, ctx)
	configureEnvironment(t, databaseURL)

	migrationConfig, err := config.LoadMigration()
	if err != nil {
		t.Fatalf("LoadMigration() error = %v", err)
	}
	db, err := platformdb.OpenPostgres(ctx, migrationConfig.Postgres)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(db.Close)
	if err := migration.Migrate(ctx, db); err != nil {
		t.Fatalf("apply coupon migration: %v", err)
	}
	if err := migration.CheckSchema(ctx, db); err != nil {
		t.Fatalf("check migrated coupon schema: %v", err)
	}

	serverConfig, err := config.LoadServer()
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	serverConfig.HTTP.PublicAddr = unusedAddress(t)
	serverConfig.HTTP.AdminAddr = unusedAddress(t)
	serverConfig.HTTP.DrainDelay = time.Millisecond
	serverConfig.Lifecycle.ShutdownTimeout = 2 * time.Second
	testServerLifecycle(t, serverConfig)

	workerConfig, err := config.LoadWorker()
	if err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
	workerConfig.AdminAddr = unusedAddress(t)
	workerConfig.Lifecycle.ShutdownTimeout = 2 * time.Second
	testWorkerLifecycle(t, workerConfig)
}

func startPostgres(t *testing.T, ctx context.Context) string {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
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

func configureEnvironment(t *testing.T, databaseURL string) {
	t.Helper()
	t.Setenv("SERVICE_ENVIRONMENT", "test")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("COUPON_REDIS_GATE_ENABLED", "false")
	t.Setenv("READINESS_CHECK_TIMEOUT", "1s")
	t.Setenv("COUPON_WORKER_POLL_INTERVAL", "20ms")
	t.Setenv("COUPON_WORKER_ATTEMPT_TIMEOUT", "100ms")
	t.Setenv("COUPON_WORKER_LEASE", "1s")
}

func testServerLifecycle(t *testing.T, cfg config.ServerConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	server, err := app.NewServer(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("NewServer() error = %v", err)
	}
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- server.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	assertOperationalEndpoints(t, cfg.HTTP.AdminAddr, cfg.Service.Name)
	cancel()
	assertStopped(t, result, "server")
}

func testWorkerLifecycle(t *testing.T, cfg config.WorkerConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	worker, err := app.NewWorker(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("NewWorker() error = %v", err)
	}
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- worker.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	assertOperationalEndpoints(t, cfg.AdminAddr, cfg.Service.Name+"-worker")
	cancel()
	assertStopped(t, result, "worker")
}

func assertOperationalEndpoints(t *testing.T, address, serviceName string) {
	t.Helper()
	for _, endpoint := range []struct {
		path         string
		bodyContains string
	}{
		{path: "/readyz", bodyContains: serviceName},
		{path: "/healthz", bodyContains: serviceName},
		{path: "/metrics", bodyContains: "service_ready"},
	} {
		waitForEndpoint(t, "http://"+address+endpoint.path, endpoint.bodyContains)
	}
}

func waitForEndpoint(t *testing.T, endpoint, bodyContains string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		response, err := client.Get(endpoint)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			switch {
			case readErr != nil:
				lastErr = readErr
			case closeErr != nil:
				lastErr = closeErr
			case response.StatusCode != http.StatusOK:
				lastErr = &unexpectedStatusError{status: response.StatusCode, body: string(body)}
			case !strings.Contains(string(body), bodyContains):
				lastErr = &missingBodyError{expected: bodyContains, body: string(body)}
			default:
				return
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("endpoint %s did not become healthy: %v", endpoint, lastErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type unexpectedStatusError struct {
	status int
	body   string
}

func (e *unexpectedStatusError) Error() string {
	return http.StatusText(e.status) + ": " + e.body
}

type missingBodyError struct {
	expected string
	body     string
}

func (e *missingBodyError) Error() string {
	return "response does not contain " + e.expected + ": " + e.body
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
