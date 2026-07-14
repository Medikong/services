package user

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/samber/oops"
)

const (
	migrationTable       = "user_goose_db_version"
	MinimumSchemaVersion = int64(1)
	MaximumSchemaVersion = int64(1)
)

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
		migrateErr = oops.In("user_migration").Code("user.migrate_failed").Wrap(migrateErr)
	}
	if closeErr != nil {
		closeErr = oops.In("user_migration").Code("user.migration_close_failed").Wrap(closeErr)
	}
	return oops.Join(migrateErr, closeErr)
}

// CheckSchema verifies the range supported by this binary. It deliberately
// does not require the database to equal the newest embedded migration.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return oops.In("user_migration").Code("user.pool_required").New("postgres pool is required")
	}
	var current int64
	err := pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0)
		FROM %s
	`, migrationTable)).Scan(&current)
	if err != nil {
		return oops.In("user_migration").Code("user.schema_unavailable").Wrap(err)
	}
	if current < MinimumSchemaVersion || current > MaximumSchemaVersion {
		return oops.In("user_migration").Code("user.schema_unsupported").
			With("current_version", current, "minimum_supported_version", MinimumSchemaVersion, "maximum_supported_version", MaximumSchemaVersion).
			New("user database schema is outside the supported range")
	}
	return nil
}

func migrationProvider(pool *pgxpool.Pool, withLock bool) (*goose.Provider, error) {
	if pool == nil {
		return nil, oops.In("user_migration").Code("user.pool_required").New("postgres pool is required")
	}
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, oops.In("user_migration").Code("user.migrations_open_failed").Wrap(err)
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
			return nil, oops.In("user_migration").Code("user.migration_lock_failed").Wrap(err)
		}
		options = append(options, goose.WithSessionLocker(locker))
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, options...)
	if err != nil {
		_ = db.Close()
		return nil, oops.In("user_migration").Code("user.migration_provider_failed").Wrap(err)
	}
	return provider, nil
}
