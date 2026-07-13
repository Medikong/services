package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/commanding"
)

type operationsCommandSourceFunc func(context.Context, commanding.OperationsCommandSubmitter) error

func (f operationsCommandSourceFunc) Run(ctx context.Context, submitter commanding.OperationsCommandSubmitter) error {
	return f(ctx, submitter)
}

func TestRunOperationsCommandSourceTreatsCallerCancellationAsNormalShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runOperationsCommandSource(ctx, operationsCommandSourceFunc(func(ctx context.Context, _ commanding.OperationsCommandSubmitter) error {
		return ctx.Err()
	}), nil)
	if err != nil {
		t.Fatalf("runOperationsCommandSource() error = %v", err)
	}
}

func TestCollectServerRuntimeResultsStopsAtShutdownTimeout(t *testing.T) {
	startedAt := time.Now()
	componentErr := errors.New("component failed")
	err := collectServerRuntimeResults(make(chan serverRuntimeResult), 0, 1, 10*time.Millisecond, componentErr)
	if err == nil || !hasErrorCode(err, "server_component_shutdown_timeout") {
		t.Fatalf("collectServerRuntimeResults() error = %v", err)
	}
	if !errors.Is(err, componentErr) {
		t.Fatalf("collectServerRuntimeResults() lost component error: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("collectServerRuntimeResults() elapsed = %v", elapsed)
	}
}

func TestRunOperationsCommandSourcePreservesUnexpectedError(t *testing.T) {
	want := errors.New("source failed")
	err := runOperationsCommandSource(context.Background(), operationsCommandSourceFunc(func(context.Context, commanding.OperationsCommandSubmitter) error {
		return want
	}), nil)
	if !errors.Is(err, want) {
		t.Fatalf("runOperationsCommandSource() error = %v", err)
	}
}
