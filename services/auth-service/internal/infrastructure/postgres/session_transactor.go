package postgres

import (
	"context"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type SessionTransactor struct {
	pool *pgxpool.Pool
}

func NewSessionTransactor(pool *pgxpool.Pool) *SessionTransactor {
	return &SessionTransactor{pool: pool}
}

func (t *SessionTransactor) WithinTransaction(ctx context.Context, run func(applicationsession.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("session_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := NewSessionTxRepositories(tx)
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("session_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

func NewSessionTxRepositories(tx pgx.Tx) applicationsession.TxRepositories {
	return applicationsession.TxRepositories{
		Sessions:      NewSessionTxRepository(tx),
		UserAuthState: NewSessionUserAuthStateReader(tx),
		Idempotency:   NewIdempotencyRepository(tx),
		Outbox:        NewOutboxAppender(tx),
		Audit:         NewAuditAppender(tx),
	}
}

var _ applicationsession.Transactor = (*SessionTransactor)(nil)
