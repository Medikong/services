//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/app"
	"github.com/Medikong/services/services/auth-service/internal/application/outboxrelay"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/google/uuid"
)

func TestWorkerPublishesDomainOutboxWhenPublisherIsInjected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := startPostgres(t, ctx)
	configureDevelopmentEnvironment(t, databaseURL)
	workerCfg, err := config.LoadWorker()
	if err != nil {
		t.Fatalf("load worker config: %v", err)
	}
	workerCfg.AdminAddr = unusedAddress(t)
	workerCfg.Lifecycle.ShutdownTimeout = 2 * time.Second
	db := migrateSchemas(t, ctx, workerCfg.Postgres)
	t.Cleanup(db.Close)

	event := outbox.Event{
		ID: uuid.New(), Type: "Auth.RegistrationLinked", AggregateType: "Registration", AggregateID: uuid.New(), Version: 1,
		Payload: json.RawMessage(`{"status":"linked"}`), CorrelationID: uuid.New(), OccurredAt: time.Now().UTC(),
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin outbox transaction: %v", err)
	}
	if err := outbox.NewPostgresRepository(db).Append(ctx, tx, event); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("append domain outbox event: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit domain outbox event: %v", err)
	}
	publisher := &outboxrelay.RecordingPublisher{}
	worker, err := app.NewWorkerWithPublisher(ctx, workerCfg, publisher)
	if err != nil {
		t.Fatalf("construct worker: %v", err)
	}
	runCtx, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- worker.Run(runCtx) }()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		var status string
		err := db.QueryRow(ctx, `SELECT publish_status FROM auth_outbox_events WHERE event_id=$1`, event.ID).Scan(&status)
		if err != nil {
			stop()
			t.Fatalf("read domain outbox status: %v", err)
		}
		if status == "published" {
			break
		}
		select {
		case <-deadline.C:
			stop()
			t.Fatal("worker did not publish injected outbox event")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if len(publisher.Events) != 1 || publisher.Events[0].ID != event.ID {
		stop()
		t.Fatalf("publisher events=%#v, want event %s", publisher.Events, event.ID)
	}
	stop()
	if err := <-result; err != nil {
		t.Fatalf("stop worker: %v", err)
	}
}
