//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	outboxinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/messaging/outbox"
	"github.com/google/uuid"
)

func TestDomainOutboxRelayPublishesOnlyAfterAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	repository := outboxinfra.NewPostgresRepository(db)
	event := domainoutbox.Event{
		ID: uuid.New(), Type: "Auth.RegistrationLinked", AggregateType: "Registration",
		AggregateID: uuid.New(), Version: 1, Payload: json.RawMessage(`{"status":"linked"}`),
		CorrelationID: uuid.New(), OccurredAt: time.Now().UTC(),
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin event transaction: %v", err)
	}
	if err := repository.Append(ctx, tx, event); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("append event: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit event: %v", err)
	}
	publisher := &outboxinfra.RecordingPublisher{}
	relay, err := outboxinfra.New(repository, publisher, outboxinfra.Config{WorkerID: "integration-worker", BatchSize: 10, PollInterval: time.Second, Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute})
	if err != nil {
		t.Fatalf("create relay: %v", err)
	}
	result, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatalf("relay event: %v", err)
	}
	if result.Claimed != 1 || result.Published != 1 || len(publisher.Events) != 1 || publisher.Events[0].ID != event.ID {
		t.Fatal("outbox relay acknowledgement does not match the expected result")
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT publish_status FROM auth_outbox_events WHERE event_id=$1`, event.ID).Scan(&status); err != nil {
		t.Fatalf("read published event: %v", err)
	}
	if status != "published" {
		t.Fatalf("event status=%q, want published", status)
	}
}
