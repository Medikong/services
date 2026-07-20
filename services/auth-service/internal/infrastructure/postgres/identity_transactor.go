package postgres

import (
	"context"

	applicationidentity "github.com/Medikong/services/services/auth-service/internal/application/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type IdentityTransactor struct {
	pool                     *pgxpool.Pool
	virtualProjectionEnabled bool
}

func NewIdentityTransactor(pool *pgxpool.Pool, virtualProjectionEnabled bool) *IdentityTransactor {
	return &IdentityTransactor{pool: pool, virtualProjectionEnabled: virtualProjectionEnabled}
}

func (t *IdentityTransactor) WithinTransaction(ctx context.Context, run func(applicationidentity.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("identity_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := applicationidentity.TxRepositories{
		Identities: NewIdentityRepository(tx),
		Challenges: NewChallengeRepository(tx, ChallengeOptions{
			VirtualProjectionEnabled: t.virtualProjectionEnabled,
		}),
		Proofs:      NewReauthenticationRepository(tx),
		Sessions:    NewSessionTxRepositories(tx),
		Idempotency: NewIdempotencyRepository(tx),
		Outbox:      NewOutboxAppender(tx),
		Audit:       NewAuditAppender(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("identity_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationidentity.Transactor = (*IdentityTransactor)(nil)
var _ applicationidentity.Repository = (*IdentityRepository)(nil)
var _ applicationidentity.ChallengeRepository = (*ChallengeRepository)(nil)
