//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	applicationintent "github.com/Medikong/services/services/auth-service/internal/application/intent"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	authmigration "github.com/Medikong/services/services/auth-service/internal/infrastructure/migration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestIntentTransactorCommitsAndRollsBackRepositoryBundle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedIntentPool(t, ctx)
	transactor := NewIntentTransactor(db)

	committedID := uuid.New()
	if err := transactor.WithinTransaction(ctx, func(repositories applicationintent.TxRepositories) error {
		return persistIntentBundle(ctx, repositories, committedID, "intent-commit")
	}); err != nil {
		t.Fatalf("commit intent bundle: %v", err)
	}
	assertIntentBundleCount(t, ctx, db, committedID, "intent-commit", 1)

	rolledBackID := uuid.New()
	rollbackErr := domainintent.ErrNotFound
	err := transactor.WithinTransaction(ctx, func(repositories applicationintent.TxRepositories) error {
		if persistErr := persistIntentBundle(ctx, repositories, rolledBackID, "intent-rollback"); persistErr != nil {
			return persistErr
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback error = %v", err)
	}
	assertIntentBundleCount(t, ctx, db, rolledBackID, "intent-rollback", 0)
}

func persistIntentBundle(ctx context.Context, repositories applicationintent.TxRepositories, intentID uuid.UUID, key string) error {
	now := time.Now().UTC()
	payloadID := uuid.New()
	if err := repositories.Intents.CreateActionPayload(ctx, domainintent.ActionPayload{
		ID: payloadID, ActionName: "purchase", Ciphertext: []byte("encrypted-action"), ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		return err
	}
	if err := repositories.Intents.Create(ctx, domainintent.CreateParams{
		ID: intentID, Channel: domainintent.ChannelWeb, ReturnPath: "/drops/one", Type: "purchase",
		ActionContext: json.RawMessage(`{"dropId":"drop"}`), OwnerProofHash: intentTestHash(intentID.String(), "owner"),
		CSRFHash: intentTestHash(intentID.String(), "csrf"), ActionPayloadID: &payloadID, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		return err
	}
	if err := repositories.Intents.BindActionPayload(ctx, intentID, payloadID); err != nil {
		return err
	}
	if err := repositories.Idempotency.CreateCompleted(ctx, domainidempotency.NewRecord(
		"create_authentication_intent", intentTestHash("create_authentication_intent"), intentTestHash(key), intentTestHash(intentID.String()), &intentID, nil, now.Add(time.Hour),
	), "AuthenticationIntent", "created"); err != nil {
		return err
	}
	return repositories.Audit.Append(ctx, "auth.intent.tested", "user", uuid.New(), intentID, map[string]string{"action": "purchase"}, key)
}

func assertIntentBundleCount(t *testing.T, ctx context.Context, db *pgxpool.Pool, intentID uuid.UUID, key string, want int) {
	t.Helper()
	queries := []struct {
		name  string
		query string
		arg   any
	}{
		{name: "intent", query: `SELECT count(*) FROM auth_authentication_intents WHERE intent_id=$1`, arg: intentID},
		{name: "idempotency", query: `SELECT count(*) FROM auth_idempotency_records WHERE resource_id=$1`, arg: intentID},
		{name: "audit", query: `SELECT count(*) FROM audit_outbox WHERE idempotency_key=$1`, arg: key},
	}
	for _, item := range queries {
		var got int
		if err := db.QueryRow(ctx, item.query, item.arg).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", item.name, err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", item.name, got, want)
		}
	}
}

func migratedIntentPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
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
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}
	t.Cleanup(db.Close)
	if err := audit.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate audit schema: %v", err)
	}
	if err := authmigration.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate auth schema: %v", err)
	}
	return db
}

func intentTestHash(values ...string) []byte {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hash.Sum(nil)
}
