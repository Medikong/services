//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Medikong/services/services/auth-service/internal/account"
	"github.com/Medikong/services/services/auth-service/internal/credential"
	"github.com/Medikong/services/services/auth-service/internal/dev"
	authpostgres "github.com/Medikong/services/services/auth-service/internal/postgres"
	"github.com/Medikong/services/services/auth-service/internal/principal"
	"github.com/Medikong/services/services/auth-service/internal/rolegrant"
	"github.com/Medikong/services/services/auth-service/internal/session"
	"github.com/Medikong/services/services/auth-service/internal/userlink"
)

type testAuthServices struct {
	db       *authpostgres.DB
	accounts account.Service
	sessions session.Service
	dev      dev.Service
}

func TestAuthPostgresCorePaths(t *testing.T) {
	ctx := context.Background()
	services := newTestAuthServices(t, ctx)

	signup, err := services.accounts.Signup(ctx, account.SignupInput{Email: "PG@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	login, err := services.accounts.Login(ctx, account.LoginInput{Email: "pg@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.UserID != signup.UserID {
		t.Fatalf("login userID=%q want %q", login.UserID, signup.UserID)
	}
	if result, err := services.sessions.Introspect(ctx, "Bearer "+login.AccessToken); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	} else if result.Principal.UserID != signup.UserID || !result.Principal.HasRole(string(rbac.RoleCustomer)) {
		t.Fatalf("principal = %+v", result.Principal)
	}

	refreshed, err := services.sessions.Refresh(ctx, session.RefreshInput{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.AccessToken == login.AccessToken || refreshed.RefreshToken == login.RefreshToken {
		t.Fatalf("tokens were not rotated")
	}
	if _, err := services.sessions.Introspect(ctx, "Bearer "+login.AccessToken); err == nil {
		t.Fatal("old access token introspected after refresh")
	}
	if _, err := services.sessions.Refresh(ctx, session.RefreshInput{RefreshToken: login.RefreshToken}); err == nil {
		t.Fatal("old refresh token succeeded after rotation")
	}
	if err := services.sessions.Logout(ctx, "Bearer "+refreshed.AccessToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := services.sessions.Introspect(ctx, "Bearer "+refreshed.AccessToken); err == nil {
		t.Fatal("introspect succeeded after logout")
	}

	revokeTarget, err := services.accounts.Login(ctx, account.LoginInput{Email: "pg@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Login() before revoke error = %v", err)
	}
	if err := services.sessions.Revoke(ctx, revokeTarget.Principal.SessionID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := services.sessions.Introspect(ctx, "Bearer "+revokeTarget.AccessToken); err == nil {
		t.Fatal("introspect succeeded after session revoke")
	}
}

func TestDevTokenIsDeterministicOnPostgres(t *testing.T) {
	ctx := context.Background()
	services := newTestAuthServices(t, ctx)

	issued, err := services.dev.IssueTestToken(ctx, dev.TestTokenInput{
		Token:  "test-customer",
		UserID: "user-test",
		Roles:  []string{string(rbac.RoleCustomer)},
	})
	if err != nil {
		t.Fatalf("IssueTestToken() error = %v", err)
	}
	if issued.AccessToken != "test-customer" {
		t.Fatalf("access token = %q", issued.AccessToken)
	}
	result, err := services.sessions.Introspect(ctx, "Bearer test-customer")
	if err != nil {
		t.Fatalf("Introspect(test token) error = %v", err)
	}
	if result.Principal.UserID != "user-test" || !result.Principal.HasRole(string(rbac.RoleCustomer)) {
		t.Fatalf("test principal = %+v", result.Principal)
	}
}

func newTestAuthServices(t *testing.T, ctx context.Context) testAuthServices {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth_service"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("postgres run: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := authpostgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("postgres open: %v", err)
	}
	t.Cleanup(func() { _ = db.SQL.Close() })
	migrations := authpostgres.MergeMigrations(
		account.Migrations,
		credential.Migrations,
		userlink.Migrations,
		rolegrant.Migrations,
		session.Migrations,
	)
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repoFactory := func(exec authpostgres.Executor) account.Repositories {
		return account.Repositories{
			Accounts:    account.NewPostgresRepository(exec),
			Credentials: credential.NewPostgresRepository(exec),
			UserLinks:   userlink.NewPostgresRepository(exec),
			RoleGrants:  rolegrant.NewPostgresRepository(exec),
			Sessions:    session.NewPostgresRepository(exec),
		}
	}
	repos := repoFactory(db.SQL)
	builder := principal.NewBuilder(repos.RoleGrants)
	return testAuthServices{
		db:       db,
		accounts: account.NewService(db, repos, repoFactory, builder),
		sessions: session.NewService(repos.Sessions, builder, nil),
		dev:      dev.NewService(db, repoFactory, builder),
	}
}
