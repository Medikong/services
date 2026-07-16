//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/operational"
	platformredis "github.com/Medikong/services/packages/go-platform/redisutil"
	"github.com/Medikong/services/services/go-reference-service/internal/app"
	"github.com/Medikong/services/services/go-reference-service/internal/domain/sample"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/config"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/observability"
	referencehttp "github.com/Medikong/services/services/go-reference-service/internal/transport/http"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

func TestFencedWriteAndAuditRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	postgresContainer, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("reference"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("postgres run: %v", err)
	}
	t.Cleanup(func() { _ = postgresContainer.Terminate(context.Background()) })
	databaseURL, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}

	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("redis run: %v", err)
	}
	t.Cleanup(func() { _ = redisContainer.Terminate(context.Background()) })
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis connection string: %v", err)
	}

	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("REDIS_URL", redisURL)
	serverCfg, err := config.LoadServer()
	if err != nil {
		t.Fatalf("server config load: %v", err)
	}
	workerCfg, err := config.LoadWorker()
	if err != nil {
		t.Fatalf("worker config load: %v", err)
	}
	metrics, err := observability.NewMetrics(serverCfg.Service.Name)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })
	db, err := platformdb.OpenPostgres(ctx, serverCfg.Postgres)
	if err != nil {
		t.Fatalf("postgres open: %v", err)
	}
	t.Cleanup(db.Close)
	if err := audit.Migrate(ctx, db); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	if err := sample.Migrate(ctx, db); err != nil {
		t.Fatalf("sample schema: %v", err)
	}
	sinkURL := createAuditSink(t, ctx, db, databaseURL)
	sinkConfig := serverCfg.Postgres
	sinkConfig.DatabaseURL = sinkURL
	sinkDB, err := platformdb.OpenPostgres(ctx, sinkConfig)
	if err != nil {
		t.Fatalf("audit sink open: %v", err)
	}
	t.Cleanup(sinkDB.Close)
	if err := audit.Migrate(ctx, sinkDB); err != nil {
		t.Fatalf("audit sink schema: %v", err)
	}
	workerCfg.Audit.SinkDatabaseURL = sinkURL
	workerCfg.AdminAddr = unusedAddress(t)
	sampleService, err := sample.New(db)
	if err != nil {
		t.Fatalf("sample service: %v", err)
	}
	redisClient, err := platformredis.Open(ctx, serverCfg.Redis)
	if err != nil {
		t.Fatalf("redis open: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })
	healthState := operational.NewHandler(operational.Config{
		Service:          serverCfg.Service.Name,
		ReadinessTimeout: serverCfg.Lifecycle.ReadinessTimeout,
		Checks: map[string]operational.Check{
			"database": db.Ping,
			"redis": func(ctx context.Context) error {
				return redisClient.Ping(ctx).Err()
			},
		},
		Metrics:  metrics.Handler(),
		SetReady: metrics.SetReady,
	})
	router, err := referencehttp.NewRouter(serverCfg, sampleService, redisClient, healthState, metrics)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	principalHeader, err := principal.EncodeHeader(principal.Principal{
		Type:   principal.TypeUser,
		UserID: "user-1",
		Roles:  []string{"customer"},
	})
	if err != nil {
		t.Fatalf("principal header: %v", err)
	}
	missingKey := httptest.NewRequest(http.MethodPost, "/v1/reference/resources/resource-missing/audit", nil)
	missingKey.Header.Set(headers.Principal, principalHeader)
	assertAPIError(t, router, missingKey, http.StatusBadRequest, "common.invalid_idempotency_key")
	assertAPIError(
		t,
		router,
		httptest.NewRequest(http.MethodGet, "/v1/reference/resources/resource-1/audit", nil),
		http.StatusMethodNotAllowed,
		"common.method_not_allowed",
	)
	assertAPIError(
		t,
		router,
		httptest.NewRequest(http.MethodGet, "/not-found", nil),
		http.StatusNotFound,
		"common.not_found",
	)

	for requestNumber := 1; requestNumber <= 2; requestNumber++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/reference/resources/resource-1/audit", nil)
		request.Header.Set(headers.Principal, principalHeader)
		request.Header.Set(headers.RequestID, "request-"+strconv.Itoa(requestNumber))
		request.Header.Set(headers.IdempotencyKey, "operation-"+strconv.Itoa(requestNumber))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d body=%s", requestNumber, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Fencing-Token") != strconv.Itoa(requestNumber) {
			t.Fatalf("request %d fencing token = %s", requestNumber, response.Header().Get("X-Fencing-Token"))
		}
	}
	duplicate := httptest.NewRequest(http.MethodPost, "/v1/reference/resources/resource-1/audit", nil)
	duplicate.Header.Set(headers.Principal, principalHeader)
	duplicate.Header.Set(headers.RequestID, "request-duplicate")
	duplicate.Header.Set(headers.IdempotencyKey, "operation-2")
	duplicateResponse := httptest.NewRecorder()
	router.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s", duplicateResponse.Code, duplicateResponse.Body.String())
	}
	otherResource := httptest.NewRequest(http.MethodPost, "/v1/reference/resources/resource-2/audit", nil)
	otherResource.Header.Set(headers.Principal, principalHeader)
	otherResource.Header.Set(headers.RequestID, "request-other-resource")
	otherResource.Header.Set(headers.IdempotencyKey, "operation-2")
	otherResourceResponse := httptest.NewRecorder()
	router.ServeHTTP(otherResourceResponse, otherResource)
	if otherResourceResponse.Code != http.StatusNoContent {
		t.Fatalf("same request ID on another resource status = %d body=%s", otherResourceResponse.Code, otherResourceResponse.Body.String())
	}

	metricResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricBody := metricResponse.Body.String()
	for _, expected := range []string{
		"distributed_lock_operations_total",
		"reference_operations_total",
		"http_server_request_duration_seconds",
		"pgxpool_max_connections",
		"db_client_connections_max",
		`http_route="/v1/reference/resources/{resourceID}/audit"`,
		`service_name="go-reference-service"`,
	} {
		if !strings.Contains(metricBody, expected) {
			t.Fatalf("metrics are missing %q:\n%s", expected, metricBody)
		}
	}

	var fence int64
	if err := db.QueryRow(ctx, "SELECT fence_token FROM reference_fenced_writes WHERE resource_id = 'resource-1'").Scan(&fence); err != nil {
		t.Fatalf("query fence: %v", err)
	}
	if fence != 2 {
		t.Fatalf("stored fence = %d, want 2", fence)
	}
	if err := sampleService.Apply(ctx, sample.Command{
		ResourceID:     "resource-1",
		FenceToken:     1,
		Actor:          audit.Actor{Type: "service", ID: "integration"},
		RequestID:      "stale-request",
		IdempotencyKey: "stale-operation",
	}); !errors.Is(err, sample.ErrStaleFence) {
		t.Fatalf("stale fenced write error = %v", err)
	}

	testAuditWorkerRelay(t, ctx, workerCfg, sinkDB)
	testAuditRetryToDead(t, ctx, db)
	testGracefulShutdown(t, serverCfg)
	testMissingSinkMigrationFailsFast(t, ctx, sinkDB, workerCfg)
	testMissingMigrationFailsFast(t, ctx, db, serverCfg)
}

func createAuditSink(t *testing.T, ctx context.Context, source *pgxpool.Pool, sourceURL string) string {
	t.Helper()
	if _, err := source.Exec(ctx, "CREATE DATABASE audit_sink"); err != nil {
		t.Fatalf("create audit sink database: %v", err)
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		t.Fatalf("parse source database URL: %v", err)
	}
	parsed.Path = "/audit_sink"
	return parsed.String()
}

func assertAPIError(t *testing.T, handler http.Handler, request *http.Request, status int, code string) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s status = %d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %d error response: %v body=%s", status, err, response.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q", body.Error.Code, code)
	}
}

func testAuditWorkerRelay(t *testing.T, ctx context.Context, cfg config.WorkerConfig, sink *pgxpool.Pool) {
	t.Helper()
	workerCtx, cancel := context.WithCancel(ctx)
	worker, err := app.NewWorker(workerCtx, cfg)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- worker.Run(workerCtx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var archived int
		err := sink.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&archived)
		if err == nil && archived == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit sink count did not reach 3: count=%d error=%v", archived, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	client := &http.Client{Timeout: time.Second}
	deadline = time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + cfg.AdminAddr + "/metrics")
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK &&
				strings.Contains(string(body), "audit_outbox_attempts_total") &&
				strings.Contains(string(body), `result="delivered"`) &&
				strings.Contains(string(body), "pgxpool_max_connections") &&
				strings.Contains(string(body), "/reference") &&
				strings.Contains(string(body), "/audit_sink") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker metrics did not expose delivered audit attempts: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	readiness, err := client.Get("http://" + cfg.AdminAddr + "/readyz")
	if err != nil {
		t.Fatalf("worker readiness: %v", err)
	}
	readinessBody, readErr := io.ReadAll(readiness.Body)
	_ = readiness.Body.Close()
	if readErr != nil {
		t.Fatalf("read worker readiness: %v", readErr)
	}
	if readiness.StatusCode != http.StatusOK {
		t.Fatalf("worker readiness status = %d body=%s", readiness.StatusCode, readinessBody)
	}
	if !strings.Contains(string(readinessBody), "source_database") || !strings.Contains(string(readinessBody), "sink_database") {
		t.Fatalf("worker readiness is missing source/sink checks: %s", readinessBody)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Worker.Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Worker.Run() did not stop within shutdown timeout")
	}
}

func testAuditRetryToDead(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	payload, err := audit.MarshalPayload(map[string]any{"reason": "integration failure"})
	if err != nil {
		t.Fatalf("marshal audit payload: %v", err)
	}
	event := audit.NewEvent(
		"reference.delivery.failed",
		1,
		audit.Actor{Type: "service", ID: "integration"},
		audit.Resource{Type: "reference_resource", ID: "resource-dead"},
		payload,
		"integration-dead",
	)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dead event transaction: %v", err)
	}
	inserted, err := audit.Append(ctx, tx, event)
	if err != nil || !inserted {
		_ = tx.Rollback(ctx)
		t.Fatalf("append dead event: inserted=%v error=%v", inserted, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit dead event: %v", err)
	}

	worker, err := audit.NewWorker(audit.WorkerConfig{
		Pool:           db,
		WorkerID:       "failing-integration-worker",
		BatchSize:      1,
		Lease:          3 * time.Second,
		PublishTimeout: time.Second,
		MaxAttempts:    2,
		BaseBackoff:    time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Publish: func(context.Context, audit.Event) error {
			return errors.New("sink unavailable")
		},
	})
	if err != nil {
		t.Fatalf("failing audit worker: %v", err)
	}
	if processed, err := worker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("first failed attempt: processed=%d error=%v", processed, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		processed, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatalf("second failed attempt: %v", err)
		}
		if processed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second failed attempt did not become available")
		}
		time.Sleep(time.Millisecond)
	}
	var status string
	var attempts int
	if err := db.QueryRow(ctx, "SELECT status, attempt_count FROM audit_outbox WHERE id = $1", event.ID).Scan(&status, &attempts); err != nil {
		t.Fatalf("query dead event: %v", err)
	}
	if status != "dead" || attempts != 2 {
		t.Fatalf("dead event status=%s attempts=%d", status, attempts)
	}
}

func testGracefulShutdown(t *testing.T, cfg config.ServerConfig) {
	t.Helper()
	cfg.HTTP.PublicAddr = unusedAddress(t)
	cfg.HTTP.AdminAddr = unusedAddress(t)
	cfg.HTTP.GRPCAddr = unusedAddress(t)
	cfg.HTTP.DrainDelay = 10 * time.Millisecond
	cfg.Lifecycle.ShutdownTimeout = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	server, err := app.NewServer(ctx, cfg)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	result := make(chan error, 1)
	go func() { result <- server.Run(ctx) }()
	connection, err := grpc.NewClient(cfg.HTTP.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("gRPC client: %v", err)
	}
	defer func() { _ = connection.Close() }()
	healthClient := grpc_health_v1.NewHealthClient(connection)
	deadline := time.Now().Add(2 * time.Second)
	for {
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		response, checkErr := healthClient.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{})
		checkCancel()
		if checkErr == nil && response.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gRPC health did not become serving: %v", checkErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Server.Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Server.Run() did not finish within shutdown timeout")
	}
}

func testMissingMigrationFailsFast(t *testing.T, ctx context.Context, db *pgxpool.Pool, cfg config.ServerConfig) {
	t.Helper()
	if _, err := db.Exec(ctx, "DROP TABLE reference_goose_db_version"); err != nil {
		t.Fatalf("drop reference migration version: %v", err)
	}
	cfg.HTTP.PublicAddr = unusedAddress(t)
	cfg.HTTP.AdminAddr = unusedAddress(t)
	cfg.HTTP.GRPCAddr = ""
	if _, err := app.NewServer(ctx, cfg); err == nil {
		t.Fatal("NewServer() error = nil without reference migration version")
	}
	assertTableMissing(t, ctx, db, "reference_goose_db_version")
}

func testMissingSinkMigrationFailsFast(t *testing.T, ctx context.Context, sink *pgxpool.Pool, cfg config.WorkerConfig) {
	t.Helper()
	if _, err := sink.Exec(ctx, "DROP TABLE audit_goose_db_version"); err != nil {
		t.Fatalf("drop sink migration version: %v", err)
	}
	if _, err := app.NewWorker(ctx, cfg); err == nil {
		t.Fatal("NewWorker() error = nil without sink migration version")
	}
	assertTableMissing(t, ctx, sink, "audit_goose_db_version")
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
