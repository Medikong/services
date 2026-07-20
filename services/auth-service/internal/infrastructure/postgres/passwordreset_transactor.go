package postgres

import (
	"context"

	applicationpasswordreset "github.com/Medikong/services/services/auth-service/internal/application/passwordreset"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PasswordResetTransactor struct {
	pool                     *pgxpool.Pool
	virtualProjectionEnabled bool
}

func NewPasswordResetTransactor(pool *pgxpool.Pool, virtualProjectionEnabled bool) *PasswordResetTransactor {
	return &PasswordResetTransactor{pool: pool, virtualProjectionEnabled: virtualProjectionEnabled}
}

func (t *PasswordResetTransactor) WithinTransaction(ctx context.Context, run func(applicationpasswordreset.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("passwordreset_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	repositories := applicationpasswordreset.TxRepositories{
		Resets:      NewPasswordResetRepository(tx),
		Challenges:  NewChallengeRepository(tx, ChallengeOptions{VirtualProjectionEnabled: t.virtualProjectionEnabled}),
		Identities:  NewIdentityRepository(tx),
		Intents:     NewIntentRepository(tx),
		Idempotency: NewIdempotencyRepository(tx),
		Sessions:    NewSessionTxRepository(tx),
		Outbox:      NewOutboxAppender(tx),
		Audit:       NewAuditAppender(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("passwordreset_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationpasswordreset.Transactor = (*PasswordResetTransactor)(nil)
