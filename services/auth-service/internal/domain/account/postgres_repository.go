package account

import (
	"context"

	"github.com/Medikong/services/services/auth-service/internal/platform/database"
)

type PostgresRepository struct {
	exec database.Executor
}

func NewPostgresRepository(exec database.Executor) PostgresRepository {
	return PostgresRepository{exec: exec}
}

func (r PostgresRepository) Create(ctx context.Context, account Account) error {
	_, err := r.exec.ExecContext(ctx, `INSERT INTO auth_accounts (auth_account_id) VALUES ($1) ON CONFLICT DO NOTHING`, account.AuthAccountID)
	return err
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_accounts (
		auth_account_id TEXT PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}
