//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain/inbox"
	"github.com/google/uuid"
)

func TestInboxDeduplicatesContextUserLinkEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	repository := inbox.NewPostgresRepository(db)
	message := inbox.Message{
		Consumer: "context_user_link", SourceEventID: uuid.New(), Type: "User.AuthLinkRequested", SchemaVersion: 1,
		BusinessKey: uuid.New(), LinkRequestID: uuid.New(), CausationID: uuid.New(),
		Payload: json.RawMessage(`{"registrationId":"opaque"}`), PayloadHash: hash32(77),
	}
	tx := beginDomainTx(t, ctx, db)
	stored, first, err := repository.Receive(ctx, tx, message)
	if err != nil || !first || stored.Status != inbox.StatusReceived {
		t.Fatalf("first inbox receive stored=%#v first=%t err=%v", stored, first, err)
	}
	if err := repository.MarkProcessed(ctx, tx, message.Consumer, message.SourceEventID); err != nil {
		t.Fatalf("mark inbox processed: %v", err)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	stored, first, err = repository.Receive(ctx, tx, message)
	if err != nil || first || stored.Status != inbox.StatusProcessed || string(stored.PayloadHash) != string(message.PayloadHash) {
		t.Fatalf("duplicate inbox receive stored=%#v first=%t err=%v", stored, first, err)
	}
	rollbackDomainTx(ctx, tx)
}
