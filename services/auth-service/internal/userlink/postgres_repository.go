package userlink

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/autherror"
	"github.com/Medikong/services/services/auth-service/internal/postgres"
)

type PostgresRepository struct {
	exec postgres.Executor
}

func NewPostgresRepository(exec postgres.Executor) PostgresRepository {
	return PostgresRepository{exec: exec}
}

func (r PostgresRepository) Create(ctx context.Context, link Link) error {
	_, err := r.exec.ExecContext(ctx, `
		INSERT INTO auth_user_links (auth_account_id, user_id)
		VALUES ($1, $2)`, link.AuthAccountID, link.UserID)
	return err
}

func (r PostgresRepository) Upsert(ctx context.Context, link Link) error {
	_, err := r.exec.ExecContext(ctx, `
		INSERT INTO auth_user_links (auth_account_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (auth_account_id) DO UPDATE SET user_id = EXCLUDED.user_id`,
		link.AuthAccountID, link.UserID)
	return err
}

func (r PostgresRepository) FindByAuthAccountID(ctx context.Context, authAccountID string) (Link, error) {
	row := r.exec.QueryRowContext(ctx, `
		SELECT auth_account_id, user_id
		FROM auth_user_links
		WHERE auth_account_id = $1`, authAccountID)
	var link Link
	if err := row.Scan(&link.AuthAccountID, &link.UserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Link{}, autherror.ErrInvalidCredentials
		}
		return Link{}, err
	}
	return link, nil
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_user_links (
		auth_user_link_id BIGSERIAL PRIMARY KEY,
		auth_account_id TEXT NOT NULL UNIQUE REFERENCES auth_accounts(auth_account_id),
		user_id TEXT NOT NULL UNIQUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`ALTER TABLE auth_user_links DROP COLUMN IF EXISTS real_name`,
}
