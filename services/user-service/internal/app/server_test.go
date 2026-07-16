package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/platform/config"
	"github.com/Medikong/services/services/user-service/internal/platform/observability"
)

func TestRunDrainsBeforeWaitingForInflightRequest(t *testing.T) {
	publicAddr := unusedAddress(t)
	adminAddr := unusedAddress(t)
	metrics, err := observability.NewMetrics("user-service-shutdown-test")
	if err != nil {
		t.Fatal(err)
	}
	health := operational.New("user-service-shutdown-test", nil)
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	publicHandler := health.RejectWhileDraining(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		w.WriteHeader(http.StatusOK)
	}))
	adminMux := http.NewServeMux()
	health.Register(adminMux)
	server := &Server{
		cfg: config.ServerConfig{
			HTTP:      config.HTTPConfig{PublicAddr: publicAddr, AdminAddr: adminAddr, DrainDelay: 0},
			Lifecycle: config.LifecycleConfig{ShutdownTimeout: 2 * time.Second},
		},
		metrics:    metrics,
		health:     health,
		publicHTTP: &http.Server{Addr: publicAddr, Handler: publicHandler, ReadHeaderTimeout: time.Second},
		adminHTTP:  &http.Server{Addr: adminAddr, Handler: adminMux, ReadHeaderTimeout: time.Second},
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(ctx) }()
	waitForHTTP(t, "http://"+adminAddr+"/readyz")

	responseResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + publicAddr + "/work")
		if err != nil {
			responseResult <- err
			return
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			responseResult <- readErr
			return
		}
		responseResult <- closeErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach handler")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for !health.Draining() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !health.Draining() {
		t.Fatal("readiness drain did not begin after cancellation")
	}
	select {
	case err := <-runResult:
		t.Fatalf("server returned before the in-flight request completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseRequest)
	if err := <-responseResult; err != nil {
		t.Fatalf("in-flight request failed: %v", err)
	}
	if err := <-runResult; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func unusedAddress(t *testing.T) string {
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

func waitForHTTP(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("endpoint did not become ready: %s", endpoint)
}
