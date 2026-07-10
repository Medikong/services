package sample

import (
	"context"
	"embed"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/samber/oops"
)

const migrationTable = "reference_goose_db_version"

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	provider, err := migrationProvider(pool, true)
	if err != nil {
		return err
	}
	_, migrateErr := provider.Up(ctx)
	closeErr := provider.Close()
	if migrateErr != nil {
		migrateErr = oops.In("reference_migration").Code("reference.migrate_failed").Wrap(migrateErr)
	}
	if closeErr != nil {
		closeErr = oops.In("reference_migration").Code("reference.migration_close_failed").Wrap(closeErr)
	}
	return oops.Join(migrateErr, closeErr)
}

func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	provider, err := migrationProvider(pool, false)
	if err != nil {
		return err
	}
	sources := provider.ListSources()
	if len(sources) == 0 {
		_ = provider.Close()
		return oops.In("reference_migration").Code("reference.migrations_empty").New("reference migrations are empty")
	}
	target := sources[len(sources)-1].Version
	var current int64
	versionErr := pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0)
		FROM reference_goose_db_version
	`).Scan(&current)
	closeErr := provider.Close()
	if versionErr != nil {
		return oops.Join(
			oops.In("reference_migration").Code("reference.schema_unavailable").Wrap(versionErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return oops.In("reference_migration").Code("reference.migration_close_failed").Wrap(closeErr)
	}
	if current != target {
		return oops.
			In("reference_migration").
			Code("reference.schema_outdated").
			With("current_version", current, "target_version", target).
			New("reference database schema is not current")
	}
	return nil
}

func migrationProvider(pool *pgxpool.Pool, withLock bool) (*goose.Provider, error) {
	if pool == nil {
		return nil, oops.In("reference_migration").Code("reference.pool_required").New("postgres pool is required")
	}
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, oops.In("reference_migration").Code("reference.migrations_open_failed").Wrap(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	options := []goose.ProviderOption{goose.WithTableName(migrationTable)}
	if withLock {
		locker, err := lock.NewPostgresSessionLocker(
			lock.WithLockTimeout(1, 300),
			lock.WithUnlockTimeout(1, 5),
		)
		if err != nil {
			_ = db.Close()
			return nil, oops.In("reference_migration").Code("reference.migration_lock_failed").Wrap(err)
		}
		options = append(options, goose.WithSessionLocker(locker))
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, options...)
	if err != nil {
		_ = db.Close()
		return nil, oops.In("reference_migration").Code("reference.migration_provider_failed").Wrap(err)
	}
	return provider, nil
}
