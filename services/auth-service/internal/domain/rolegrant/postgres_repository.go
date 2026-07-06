package rolegrant

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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

func (r PostgresRepository) Grant(ctx context.Context, grant Grant) error {
	_, err := r.exec(ctx, `
		INSERT INTO role_grants (auth_account_id, role)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, grant.AuthAccountID, grant.Role)
	return err
}

func (r PostgresRepository) Replace(ctx context.Context, authAccountID string, roles []string) error {
	if _, err := r.exec(ctx, `DELETE FROM role_grants WHERE auth_account_id = $1`, authAccountID); err != nil {
		return err
	}
	for _, role := range roles {
		if err := r.Grant(ctx, Grant{AuthAccountID: authAccountID, Role: role}); err != nil {
			return err
		}
	}
	return nil
}

func (r PostgresRepository) ListByAuthAccountID(ctx context.Context, authAccountID string) ([]string, error) {
	rows, err := r.query(ctx, `SELECT role FROM role_grants WHERE auth_account_id = $1 ORDER BY role`, authAccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roles, nil
}

func (r PostgresRepository) exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if r.tx != nil {
		return r.tx.Exec(ctx, sql, args...)
	}
	return r.pool.Exec(ctx, sql, args...)
}

func (r PostgresRepository) query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if r.tx != nil {
		return r.tx.Query(ctx, sql, args...)
	}
	return r.pool.Query(ctx, sql, args...)
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS role_grants (
		auth_account_id TEXT NOT NULL REFERENCES auth_accounts(auth_account_id),
		role TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (auth_account_id, role)
	)`,
}
