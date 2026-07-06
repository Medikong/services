package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/account"
	"github.com/Medikong/services/services/auth-service/internal/config"
	"github.com/Medikong/services/services/auth-service/internal/credential"
	"github.com/Medikong/services/services/auth-service/internal/dev"
	authhttp "github.com/Medikong/services/services/auth-service/internal/http"
	"github.com/Medikong/services/services/auth-service/internal/postgres"
	"github.com/Medikong/services/services/auth-service/internal/principal"
	"github.com/Medikong/services/services/auth-service/internal/rolegrant"
	"github.com/Medikong/services/services/auth-service/internal/session"
	"github.com/Medikong/services/services/auth-service/internal/userlink"
)

type App struct {
	server *http.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return App{}, fmt.Errorf("DATABASE_URL is required for auth-service")
	}

	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return App{}, err
	}
	migrations := postgres.MergeMigrations(
		account.Migrations,
		credential.Migrations,
		userlink.Migrations,
		rolegrant.Migrations,
		session.Migrations,
	)
	if err := db.Migrate(ctx, migrations); err != nil {
		_ = db.SQL.Close()
		return App{}, err
	}

	repoFactory := func(exec postgres.Executor) account.Repositories {
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
	var cache principal.AuthzCache
	if cfg.AuthzCacheEnabled {
		cache = principal.NewMemoryAuthzCache()
	}
	accountService := account.NewService(db, repos, repoFactory, builder)
	sessionService := session.NewService(repos.Sessions, builder, cache)
	devService := dev.NewService(db, repoFactory, builder)

	mux := http.NewServeMux()
	authhttp.RegisterRoutes(mux, authhttp.Services{
		Accounts: accountService,
		Sessions: sessionService,
		Dev:      devService,
	}, map[string]operational.Check{
		"database": db.Ping,
	})

	return App{
		server: httpserver.New(cfg.HTTPAddr, telemetry.Middleware(config.ServiceName, mux)),
	}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}
