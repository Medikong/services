package postgres

import (
	"context"

	applicationintent "github.com/Medikong/services/services/auth-service/internal/application/intent"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type IntentTransactor struct {
	pool *pgxpool.Pool
}

func NewIntentTransactor(pool *pgxpool.Pool) *IntentTransactor {
	return &IntentTransactor{pool: pool}
}

func (t *IntentTransactor) WithinTransaction(ctx context.Context, run func(applicationintent.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("intent_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := applicationintent.TxRepositories{
		Intents:     NewIntentRepository(tx),
		Idempotency: NewIdempotencyRepository(tx),
		Audit:       NewAuditAppender(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("intent_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var (
	_ applicationintent.Repository            = (*IntentRepository)(nil)
	_ applicationintent.IdempotencyRepository = (*IdempotencyRepository)(nil)
	_ applicationintent.AuditAppender         = (*AuditAppender)(nil)
	_ applicationintent.Transactor            = (*IntentTransactor)(nil)
)
