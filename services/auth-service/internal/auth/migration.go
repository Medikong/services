package auth

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

const (
	migrationTable    = "auth_goose_db_version"
	devMigrationTable = "auth_dev_goose_db_version"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed devmigrations/*.sql
var developmentMigrationsFS embed.FS

// Migrate applies the production-safe authentication schema. Development-only
// projections deliberately live in MigrateDevelopment so production databases
// cannot gain virtual verification data by accident.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate(ctx, pool, migrationsFS, "migrations", migrationTable, "auth_migration")
}

// CheckSchema refuses to start against a missing or outdated production schema.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return checkSchema(ctx, pool, migrationsFS, "migrations", migrationTable, "auth_migration")
}

// MigrateDevelopment applies only the local/test virtual verification projection.
// Call Migrate first; this migration has a foreign key to the core challenge table.
func MigrateDevelopment(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate(ctx, pool, developmentMigrationsFS, "devmigrations", devMigrationTable, "auth_development_migration")
}

// CheckDevelopmentSchema verifies the separately versioned virtual projection.
func CheckDevelopmentSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return checkSchema(ctx, pool, developmentMigrationsFS, "devmigrations", devMigrationTable, "auth_development_migration")
}

func migrate(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, directory, table, scope string) error {
	provider, err := migrationProvider(pool, migrationsFS, directory, table, true, scope)
	if err != nil {
		return err
	}
	_, migrateErr := provider.Up(ctx)
	closeErr := provider.Close()
	if migrateErr != nil {
		migrateErr = oops.In(scope).Code("auth.migrate_failed").Wrap(migrateErr)
	}
	if closeErr != nil {
		closeErr = oops.In(scope).Code("auth.migration_close_failed").Wrap(closeErr)
	}
	return oops.Join(migrateErr, closeErr)
}

func checkSchema(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, directory, table, scope string) error {
	provider, err := migrationProvider(pool, migrationsFS, directory, table, false, scope)
	if err != nil {
		return err
	}
	sources := provider.ListSources()
	if len(sources) == 0 {
		_ = provider.Close()
		return oops.In(scope).Code("auth.migrations_empty").New("authentication migrations are empty")
	}

	var current int64
	versionErr := pool.QueryRow(ctx, migrationVersionQuery(table)).Scan(&current)
	closeErr := provider.Close()
	if versionErr != nil {
		return oops.Join(
			oops.In(scope).Code("auth.schema_unavailable").Wrap(versionErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return oops.In(scope).Code("auth.migration_close_failed").Wrap(closeErr)
	}

	target := sources[len(sources)-1].Version
	if current != target {
		return oops.
			In(scope).
			Code("auth.schema_outdated").
			With("current_version", current, "target_version", target).
			New("authentication database schema is not current")
	}
	return nil
}

func migrationProvider(pool *pgxpool.Pool, migrationsFS fs.FS, directory, table string, withLock bool, scope string) (*goose.Provider, error) {
	if pool == nil {
		return nil, oops.In(scope).Code("auth.pool_required").New("postgres pool is required")
	}
	migrations, err := fs.Sub(migrationsFS, directory)
	if err != nil {
		return nil, oops.In(scope).Code("auth.migrations_open_failed").Wrap(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	options := []goose.ProviderOption{goose.WithTableName(table)}
	if withLock {
		locker, err := lock.NewPostgresSessionLocker(
			lock.WithLockTimeout(1, 300),
			lock.WithUnlockTimeout(1, 5),
		)
		if err != nil {
			_ = db.Close()
			return nil, oops.In(scope).Code("auth.migration_lock_failed").Wrap(err)
		}
		options = append(options, goose.WithSessionLocker(locker))
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, options...)
	if err != nil {
		_ = db.Close()
		return nil, oops.In(scope).Code("auth.migration_provider_failed").Wrap(err)
	}
	return provider, nil
}

func migrationVersionQuery(table string) string {
	switch table {
	case migrationTable:
		return `
			SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0)
			FROM auth_goose_db_version
		`
	case devMigrationTable:
		return `
			SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0)
			FROM auth_dev_goose_db_version
		`
	default:
		panic("unknown authentication migration table")
	}
}
