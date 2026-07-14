package app

import (
	"context"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/user-service/internal/platform/config"
)

type Resources struct {
	DB *pgxpool.Pool
}

func openServerResources(ctx context.Context, cfg config.ServerConfig) (Resources, error) {
	db, err := platformdb.OpenPostgres(ctx, cfg.Postgres)
	if err != nil {
		return Resources{}, oops.In("user_resources").Code("database.open_failed").Wrap(err)
	}
	return Resources{DB: db}, nil
}

func (r *Resources) Close() error {
	if r.DB != nil {
		r.DB.Close()
		r.DB = nil
	}
	return nil
}
