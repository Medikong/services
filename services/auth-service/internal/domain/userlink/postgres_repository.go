package userlink

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("auth user link not found")

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

func (r PostgresRepository) Create(ctx context.Context, link Link) error {
	_, err := r.exec(ctx, `
		INSERT INTO auth_user_links (auth_account_id, user_id)
		VALUES ($1, $2)`, link.AuthAccountID, link.UserID)
	return err
}

func (r PostgresRepository) Upsert(ctx context.Context, link Link) error {
	_, err := r.exec(ctx, `
		INSERT INTO auth_user_links (auth_account_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (auth_account_id) DO UPDATE
		SET user_id = EXCLUDED.user_id, updated_at = now()`,
		link.AuthAccountID, link.UserID)
	return err
}

func (r PostgresRepository) FindByAuthAccountID(ctx context.Context, authAccountID string) (Link, error) {
	row := r.queryRow(ctx, `
		SELECT auth_user_link_id, auth_account_id, user_id, created_at, updated_at
		FROM auth_user_links
		WHERE auth_account_id = $1`, authAccountID)
	var link Link
	if err := row.Scan(&link.AuthUserLinkID, &link.AuthAccountID, &link.UserID, &link.CreatedAt, &link.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, err
	}
	return link, nil
}

func (r PostgresRepository) exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if r.tx != nil {
		return r.tx.Exec(ctx, sql, args...)
	}
	return r.pool.Exec(ctx, sql, args...)
}

func (r PostgresRepository) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if r.tx != nil {
		return r.tx.QueryRow(ctx, sql, args...)
	}
	return r.pool.QueryRow(ctx, sql, args...)
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_user_links (
			auth_user_link_id BIGSERIAL PRIMARY KEY,
			auth_account_id TEXT NOT NULL UNIQUE REFERENCES auth_accounts(auth_account_id),
			user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	`ALTER TABLE auth_user_links DROP COLUMN IF EXISTS real_name`,
	`ALTER TABLE auth_user_links ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
	`ALTER TABLE auth_user_links DROP CONSTRAINT IF EXISTS auth_user_links_user_id_key`,
	`CREATE INDEX IF NOT EXISTS auth_user_links_user_id_idx ON auth_user_links(user_id)`,
}
