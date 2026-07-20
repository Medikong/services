package postgres

import (
	"context"

	applicationreauth "github.com/Medikong/services/services/auth-service/internal/application/reauth"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type ReauthenticationTransactor struct {
	pool *pgxpool.Pool
}

func NewReauthenticationTransactor(pool *pgxpool.Pool) *ReauthenticationTransactor {
	return &ReauthenticationTransactor{pool: pool}
}

func (t *ReauthenticationTransactor) WithinTransaction(ctx context.Context, run func(applicationreauth.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("reauthentication_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := applicationreauth.TxRepositories{
		Identities:  NewIdentityRepository(tx),
		Proofs:      NewReauthenticationRepository(tx),
		Sessions:    NewSessionTxRepositories(tx),
		Idempotency: NewIdempotencyRepository(tx),
		Audit:       NewAuditAppender(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("reauthentication_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationreauth.Transactor = (*ReauthenticationTransactor)(nil)
var _ applicationreauth.IdentityReader = (*IdentityRepository)(nil)
