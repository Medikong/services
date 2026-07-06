//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/backoffice-service/internal/domain/drop"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type fakeCouponClient struct{}

func (fakeCouponClient) PreparePolicy(context.Context, drop.PrepareDropInput) error {
	return nil
}

func TestBackofficePostgresPrepareReadiness(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("backoffice_service"),
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
	store, err := drop.OpenPostgresRepository(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	svc := drop.NewService(store, fakeCouponClient{})
	readiness, err := svc.PrepareDrop(ctx, principal.Principal{Type: principal.TypeUser, UserID: "op-1", Roles: []string{"operator"}}, drop.PrepareDropInput{
		ProductID:     "product-pg-1",
		ProductName:   "PG Hoodie",
		DropID:        "drop-pg-1",
		SaleStartsAt:  "2026-07-05T10:00:00Z",
		StockQuantity: 10,
		CouponPolicy:  drop.CouponPolicyInput{PolicyID: "policy-pg-1", Name: "Launch", TotalQuantity: 5},
	})
	if err != nil {
		t.Fatalf("PrepareDrop() error = %v", err)
	}
	if !readiness.Ready {
		t.Fatalf("readiness=%+v", readiness)
	}
}
