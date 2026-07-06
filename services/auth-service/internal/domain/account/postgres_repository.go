package account

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlreadyExists = errors.New("auth account already exists")
	ErrNotFound      = errors.New("auth account not found")
)

type PostgresRepository struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

func NewPostgresRepository(pool *pgxpool.Pool) PostgresRepository {
	return PostgresRepository{pool: pool}
}

func NewPostgresTxRepository(tx pgx.Tx) PostgresRepository {
	return PostgresRepository{tx: tx}
}

func (r PostgresRepository) Create(ctx context.Context, account Account) (Account, error) {
	if err := account.Validate(); err != nil {
		return Account{}, err
	}
	row := r.queryRow(ctx, `
		INSERT INTO auth_accounts (auth_account_id, status)
		VALUES ($1, $2)
		RETURNING auth_account_id, status, created_at, updated_at`,
		account.AuthAccountID, account.statusOrDefault())
	created, err := scanAccount(row)
	if isUniqueViolation(err) {
		return Account{}, ErrAlreadyExists
	}
	return created, err
}

func (r PostgresRepository) Ensure(ctx context.Context, account Account) (Account, error) {
	if err := account.Validate(); err != nil {
		return Account{}, err
	}
	row := r.queryRow(ctx, `
		INSERT INTO auth_accounts (auth_account_id, status)
		VALUES ($1, $2)
		ON CONFLICT (auth_account_id) DO UPDATE
		SET status = EXCLUDED.status, updated_at = now()
		RETURNING auth_account_id, status, created_at, updated_at`,
		account.AuthAccountID, account.statusOrDefault())
	return scanAccount(row)
}

func (r PostgresRepository) FindByID(ctx context.Context, authAccountID string) (Account, error) {
	row := r.queryRow(ctx, `
		SELECT auth_account_id, status, created_at, updated_at
		FROM auth_accounts
		WHERE auth_account_id = $1`, authAccountID)
	account, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return account, err
}

type accountScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row accountScanner) (Account, error) {
	var account Account
	if err := row.Scan(&account.AuthAccountID, &account.Status, &account.CreatedAt, &account.UpdatedAt); err != nil {
		return Account{}, err
	}
	return account, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r PostgresRepository) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if r.tx != nil {
		return r.tx.QueryRow(ctx, sql, args...)
	}
	return r.pool.QueryRow(ctx, sql, args...)
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_accounts (
		auth_account_id TEXT PRIMARY KEY,
		status TEXT NOT NULL DEFAULT 'active',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`ALTER TABLE auth_accounts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'`,
	`ALTER TABLE auth_accounts ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
	`DO $$
	BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'auth_accounts_status_check'
		) THEN
			ALTER TABLE auth_accounts
			ADD CONSTRAINT auth_accounts_status_check CHECK (status IN ('active', 'disabled'));
		END IF;
	END $$`,
}
