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

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Medikong/services/services/auth-service/internal/app"
)

func TestServerRuntimeWithPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("postgres run: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
	cfg := loadProductionHTTPServerConfig(t, databaseURL)
	db := migrateProductionSchemas(t, ctx, cfg.Postgres)
	t.Cleanup(db.Close)
	server, err := app.NewServer(ctx, cfg, app.ServerOptions{})
	if err != nil {
		t.Fatalf("new server: %s", runtimeRedact(err.Error(), databaseURL))
	}
	runCtx, stop := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(runCtx) }()
	runtimeWaitForStatus(t, "http://"+cfg.HTTP.AdminAddr+"/readyz", http.StatusOK)
	runtimeWaitForStatus(t, "http://"+cfg.HTTP.PublicAddr+"/.well-known/jwks.json", http.StatusOK)

	response, err := http.Get("http://" + cfg.HTTP.AdminAddr + "/metrics")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		stop()
		t.Fatalf("metrics response read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "service_ready") {
		stop()
		t.Fatalf("metrics status=%d contains service_ready=%v", response.StatusCode, strings.Contains(string(body), "service_ready"))
	}
	stop()
	if err := <-runResult; err != nil {
		t.Fatalf("server run: %v", err)
	}
}

func runtimeUnusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func runtimeWaitForStatus(t *testing.T, endpoint string, expected int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == expected {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("endpoint %s did not return %d", endpoint, expected)
}

func runtimeRedact(value, secret string) string {
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
