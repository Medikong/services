package postgres

import (
	"context"

	applicationoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type OperatorTransactor struct {
	pool *pgxpool.Pool
}

func NewOperatorTransactor(pool *pgxpool.Pool) *OperatorTransactor {
	return &OperatorTransactor{pool: pool}
}

func (t *OperatorTransactor) WithinTransaction(ctx context.Context, run func(applicationoperator.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("operator_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := applicationoperator.TxRepositories{
		Operators:   NewOperatorRepository(tx),
		Policies:    NewPolicyRepository(tx),
		Sessions:    NewSessionTxRepository(tx),
		Idempotency: NewIdempotencyRepository(tx),
		Outbox:      NewOutboxAppender(tx),
		Audit:       NewAuditAppender(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("operator_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationoperator.Transactor = (*OperatorTransactor)(nil)
