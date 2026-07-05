//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/user-service/internal/model"
	userservice "github.com/Medikong/services/services/user-service/internal/service"
	postgresstore "github.com/Medikong/services/services/user-service/internal/store/postgres"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestUserPostgresEnsureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("user_service"),
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
	svc := userservice.New(store)
	if _, err := svc.Ensure(ctx, userservice.EnsureInput{UserID: "user-pg-1"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	realName := "PG User"
	nickname := "pg-user"
	if _, err := svc.UpdateMyProfile(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-pg-1"}, model.ProfileUpdate{RealName: &realName, Nickname: &nickname}); err != nil {
		t.Fatalf("UpdateMyProfile() error = %v", err)
	}
	user, err := svc.Me(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-pg-1"})
	if err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if user.RealName != "PG User" {
		t.Fatalf("realName=%q", user.RealName)
	}
}
