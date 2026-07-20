package postgres

import (
	"context"

	applicationdevelopment "github.com/Medikong/services/services/auth-service/internal/application/development"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type DevelopmentTransactor struct {
	pool *pgxpool.Pool
}

func NewDevelopmentTransactor(pool *pgxpool.Pool) *DevelopmentTransactor {
	return &DevelopmentTransactor{pool: pool}
}

func (t *DevelopmentTransactor) WithinTransaction(ctx context.Context, run func(applicationdevelopment.Repository) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("development_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := run(NewDevelopmentRepository(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("development_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationdevelopment.Transactor = (*DevelopmentTransactor)(nil)
