//go:build integration

package eventing_test

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresOutboxPreservesAggregateEventOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_outbox"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migration.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstID, secondID, thirdID, otherID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, row := range []struct {
		id        uuid.UUID
		aggregate string
		version   int
		occurred  time.Time
	}{
		{firstID, "ucpn_order001", 1, now},
		{secondID, "ucpn_order001", 2, now.Add(time.Microsecond)},
		{thirdID, "ucpn_order001", 2, now.Add(2 * time.Microsecond)},
		{otherID, "ucpn_order002", 1, now.Add(3 * time.Microsecond)},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO domain_outbox (
			event_id,event_type,event_document_id,aggregate_type,aggregate_id,aggregate_version,
			correlation_id,payload,occurred_at
		) VALUES ($1,'coupon.test','EVT.A.19-09','UserCoupon',$2,$3,'correlation','{}',$4)`,
			row.id, row.aggregate, row.version, row.occurred)
		if err != nil {
			t.Fatal(err)
		}
	}
	repo := eventing.NewPostgresOutbox(pool)
	claimed, err := repo.Claim(ctx, "worker-1", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimedIDs := make(map[uuid.UUID]bool, len(claimed))
	for _, item := range claimed {
		claimedIDs[item.Envelope.EventID] = true
	}
	if len(claimed) != 2 || !claimedIDs[firstID] || !claimedIDs[otherID] || claimedIDs[secondID] {
		t.Fatalf("first claim = %#v", claimed)
	}
	if err := repo.MarkPublished(ctx, firstID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.Claim(ctx, "worker-2", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Envelope.EventID != secondID {
		t.Fatalf("second claim = %#v", claimed)
	}
	if err := repo.MarkPublished(ctx, secondID, "worker-2"); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.Claim(ctx, "worker-3", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Envelope.EventID != thirdID {
		t.Fatalf("third claim = %#v", claimed)
	}
}
