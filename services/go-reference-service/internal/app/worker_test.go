package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/config"
)

func TestWorkerRunContinuesAfterCleanupError(t *testing.T) {
	originalLogger := logger.Default()
	logger.Configure(io.Discard, "worker-test")
	t.Cleanup(func() { logger.SetDefault(originalLogger) })

	cleanupCalledTwice := make(chan struct{})
	cleanupCalls := 0
	relayStopped := make(chan struct{})
	worker := &Worker{
		cfg: config.WorkerConfig{
			Audit: config.AuditConfig{
				PublishTimeout: 50 * time.Millisecond,
			},
			Lifecycle: config.LifecycleConfig{
				ShutdownTimeout: time.Second,
			},
		},
		runAudit: func(ctx context.Context) error {
			<-ctx.Done()
			close(relayStopped)
			return nil
		},
		cleanup: func(context.Context) (int64, error) {
			cleanupCalls++
			if cleanupCalls == 2 {
				close(cleanupCalledTwice)
			}
			return 0, errors.New("cleanup failed")
		},
		cleanupInterval: time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- worker.Run(ctx) }()

	select {
	case <-cleanupCalledTwice:
	case <-time.After(time.Second):
		t.Fatal("Run() did not continue after cleanup error")
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
	select {
	case <-relayStopped:
	default:
		t.Fatal("relay was not stopped before Run returned")
	}
}

func TestWorkerRunBoundsShutdownWait(t *testing.T) {
	releaseRelay := make(chan struct{})
	worker := &Worker{
		cfg: config.WorkerConfig{
			Lifecycle: config.LifecycleConfig{
				ShutdownTimeout: 20 * time.Millisecond,
			},
		},
		runAudit: func(context.Context) error {
			<-releaseRelay
			return nil
		},
		cleanup:         func(context.Context) (int64, error) { return 0, nil },
		cleanupInterval: time.Hour,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	startedAt := time.Now()
	err := worker.Run(ctx)
	close(releaseRelay)

	if err == nil {
		t.Fatal("Run() error = nil, want shutdown timeout")
	}
	if !strings.Contains(err.Error(), "shutdown timeout") {
		t.Fatalf("Run() error = %v, want shutdown timeout", err)
	}
	if time.Since(startedAt) >= time.Second {
		t.Fatalf("Run() exceeded bounded shutdown wait: %s", time.Since(startedAt))
	}
}
