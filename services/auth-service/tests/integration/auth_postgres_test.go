//go:build integration

package integration_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Medikong/services/packages/go-authz/rbac"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Medikong/services/services/auth-service/internal/domain/account"
	"github.com/Medikong/services/services/auth-service/internal/domain/dev"
	"github.com/Medikong/services/services/auth-service/internal/domain/passwordauth"
	"github.com/Medikong/services/services/auth-service/internal/domain/principal"
	"github.com/Medikong/services/services/auth-service/internal/domain/providerlink"
	"github.com/Medikong/services/services/auth-service/internal/domain/rolegrant"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userlink"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testAuthServices struct {
	db       *pgxpool.Pool
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
	accountRepo := account.NewPostgresRepository(services.db)
	storedAccount, err := accountRepo.FindByID(ctx, signup.AuthAccountID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	if storedAccount.Status != account.StatusActive || storedAccount.CreatedAt.IsZero() || storedAccount.UpdatedAt.IsZero() {
		t.Fatalf("stored account = %+v", storedAccount)
	}
	providerRepo := providerlink.NewPostgresRepository(services.db)
	providerLink, err := providerRepo.Create(ctx, providerlink.Link{
		AuthAccountID:         signup.AuthAccountID,
		AuthProvider:          "google",
		ProviderSubject:       "google-subject-1",
		ProviderEmail:         "pg@example.com",
		ProviderEmailVerified: true,
	})
	if err != nil {
		t.Fatalf("provider link create: %v", err)
	}
	foundProviderLink, err := providerRepo.FindByProviderSubject(ctx, "google", "google-subject-1")
	if err != nil {
		t.Fatalf("provider link find: %v", err)
	}
	if foundProviderLink.ProviderLinkID != providerLink.ProviderLinkID || foundProviderLink.AuthAccountID != signup.AuthAccountID {
		t.Fatalf("provider link = %+v want %+v", foundProviderLink, providerLink)
	}
	secondAccount, err := account.New("auth_second_for_same_user")
	if err != nil {
		t.Fatalf("account.New() error = %v", err)
	}
	if _, err := accountRepo.Create(ctx, secondAccount); err != nil {
		t.Fatalf("second account create: %v", err)
	}
	if err := userlink.NewPostgresRepository(services.db).Create(ctx, userlink.Link{
		AuthAccountID: secondAccount.AuthAccountID,
		UserID:        signup.UserID,
	}); err != nil {
		t.Fatalf("second auth account link to same user: %v", err)
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
	db, err := platformdb.OpenPostgres(ctx, platformdb.DefaultPostgresConfig(dsn))
	if err != nil {
		t.Fatalf("postgres open: %v", err)
	}
	t.Cleanup(db.Close)
	migrations := slices.Concat(
		account.Migrations,
		passwordauth.Migrations,
		providerlink.Migrations,
		userlink.Migrations,
		rolegrant.Migrations,
		session.Migrations,
	)
	if err := platformdb.RunMigrations(ctx, db, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repoFactory := func(tx pgx.Tx) account.Repositories {
		return account.Repositories{
			Accounts:      account.NewPostgresTxRepository(tx),
			PasswordAuth:  passwordauth.NewPostgresTxRepository(tx),
			ProviderLinks: providerlink.NewPostgresTxRepository(tx),
			UserLinks:     userlink.NewPostgresTxRepository(tx),
			RoleGrants:    rolegrant.NewPostgresTxRepository(tx),
			Sessions:      session.NewPostgresTxRepository(tx),
		}
	}
	repos := account.Repositories{
		Accounts:      account.NewPostgresRepository(db),
		PasswordAuth:  passwordauth.NewPostgresRepository(db),
		ProviderLinks: providerlink.NewPostgresRepository(db),
		UserLinks:     userlink.NewPostgresRepository(db),
		RoleGrants:    rolegrant.NewPostgresRepository(db),
		Sessions:      session.NewPostgresRepository(db),
	}
	builder := principal.NewBuilder(repos.RoleGrants)
	return testAuthServices{
		db:       db,
		accounts: account.NewService(db, repos, repoFactory, builder),
		sessions: session.NewService(repos.Sessions, builder, nil),
		dev:      dev.NewService(db, repoFactory, builder),
	}
}
