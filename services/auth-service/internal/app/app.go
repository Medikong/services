package app

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/domain/account"
	"github.com/Medikong/services/services/auth-service/internal/domain/dev"
	"github.com/Medikong/services/services/auth-service/internal/domain/passwordauth"
	"github.com/Medikong/services/services/auth-service/internal/domain/principal"
	"github.com/Medikong/services/services/auth-service/internal/domain/providerlink"
	"github.com/Medikong/services/services/auth-service/internal/domain/rolegrant"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userlink"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	authhttp "github.com/Medikong/services/services/auth-service/internal/transport/http"
	"github.com/jackc/pgx/v5"
)

type App struct {
	server *http.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return App{}, fmt.Errorf("DATABASE_URL is required for auth-service")
	}

	db, err := platformdb.OpenPostgres(ctx, cfg.Postgres)
	if err != nil {
		return App{}, err
	}
	migrations := slices.Concat(
		account.Migrations,
		passwordauth.Migrations,
		providerlink.Migrations,
		userlink.Migrations,
		rolegrant.Migrations,
		session.Migrations,
	)
	if err := platformdb.RunMigrations(ctx, db, migrations); err != nil {
		db.Close()
		return App{}, err
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
	tokens, err := session.NewTokenManager(session.TokenConfig{
		Issuer:          cfg.JWTIssuer,
		Secret:          cfg.JWTSecret,
		AccessTokenTTL:  cfg.AccessTokenTTL,
		RefreshTokenTTL: cfg.RefreshTokenTTL,
	})
	if err != nil {
		db.Close()
		return App{}, err
	}
	accountService := account.NewService(db, repos, repoFactory, builder, tokens)
	sessionService := session.NewService(repos.Sessions, builder, tokens)
	devService := dev.NewService(db, repoFactory, builder, tokens, cfg.DevTestToken)

	runtimeMetrics := metrics.NewRegistry()
	router := authhttp.NewRouter(authhttp.Services{
		Accounts: accountService,
		Sessions: sessionService,
		Dev:      devService,
	}, map[string]operational.Check{
		"database": db.Ping,
	}, runtimeMetrics)
	handler := httpmiddleware.Stack(httpmiddleware.Config{
		ServiceName:  config.ServiceName,
		Metrics:      runtimeMetrics,
		RoutePattern: authhttp.RoutePattern,
	}, router)

	return App{
		server: httpserver.New(cfg.HTTPAddr, handler),
	}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}
