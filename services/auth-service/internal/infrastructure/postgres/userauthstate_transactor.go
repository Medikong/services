package postgres

import (
	"context"

	applicationuserauthstate "github.com/Medikong/services/services/auth-service/internal/application/userauthstate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type UserAuthStateTransactor struct {
	pool *pgxpool.Pool
}

func NewUserAuthStateTransactor(pool *pgxpool.Pool) *UserAuthStateTransactor {
	return &UserAuthStateTransactor{pool: pool}
}

func (t *UserAuthStateTransactor) WithinTransaction(ctx context.Context, run func(applicationuserauthstate.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("userauthstate_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := applicationuserauthstate.TxRepositories{
		States:   NewUserAuthStateRepository(tx),
		Sessions: NewUserSessionRevoker(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("userauthstate_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationuserauthstate.Transactor = (*UserAuthStateTransactor)(nil)
