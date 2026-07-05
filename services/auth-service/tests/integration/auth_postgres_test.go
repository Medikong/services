//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	authservice "github.com/Medikong/services/services/auth-service/internal/service"
	postgresstore "github.com/Medikong/services/services/auth-service/internal/store/postgres"
)

func TestAuthPostgresSignupLogin(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth_service"),
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
	svc := authservice.New(store)
	signup, err := svc.Signup(ctx, authservice.SignupInput{Email: "pg@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	login, err := svc.Login(ctx, authservice.LoginInput{Email: "pg@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.UserID != signup.UserID {
		t.Fatalf("login userID=%q want %q", login.UserID, signup.UserID)
	}
	refreshed, err := svc.Refresh(ctx, authservice.RefreshInput{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.AccessToken == login.AccessToken || refreshed.RefreshToken == login.RefreshToken {
		t.Fatalf("tokens were not rotated")
	}
	if _, err := svc.Introspect(ctx, "Bearer "+login.AccessToken); err == nil {
		t.Fatal("old access token introspected after refresh")
	}
	if err := svc.Logout(ctx, "Bearer "+refreshed.AccessToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := svc.Introspect(ctx, "Bearer "+refreshed.AccessToken); err == nil {
		t.Fatal("introspect succeeded after logout")
	}
}
