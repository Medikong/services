//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Medikong/services/services/coupon-service/internal/repository"
	postgresstore "github.com/Medikong/services/services/coupon-service/internal/store/postgres"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCouponPostgresConcurrentIssueRespectsQuantity(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_service"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("postgres run: %v", err)
	}
	defer func() { _ = container.Terminate(ctx) }()
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	store, err := postgresstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	if _, err := store.UpsertPolicy(ctx, repository.PolicyInput{PolicyID: "policy-pg-1", DropID: "drop-pg-1", Name: "Launch", TotalQuantity: 5, Status: "ready"}); err != nil {
		t.Fatalf("UpsertPolicy() error = %v", err)
	}

	var issued atomic.Int32
	var soldOut atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := store.Issue(ctx, "policy-pg-1", "user-pg-"+string(rune('a'+index)), "")
			if err == nil {
				issued.Add(1)
				return
			}
			if errors.Is(err, repository.ErrSoldOut) {
				soldOut.Add(1)
				return
			}
			t.Errorf("Issue() error = %v", err)
		}(i)
	}
	wg.Wait()
	if issued.Load() != 5 || soldOut.Load() != 15 {
		t.Fatalf("issued=%d soldOut=%d, want 5/15", issued.Load(), soldOut.Load())
	}
	policy, err := store.GetPolicy(ctx, "policy-pg-1")
	if err != nil {
		t.Fatalf("GetPolicy() error = %v", err)
	}
	if policy.IssuedCount != 5 {
		t.Fatalf("issuedCount=%d, want 5", policy.IssuedCount)
	}
}
