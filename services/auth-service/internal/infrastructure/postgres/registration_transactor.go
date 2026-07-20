package postgres

import (
	"context"

	applicationregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type RegistrationTransactor struct {
	pool                     *pgxpool.Pool
	virtualProjectionEnabled bool
}

func NewRegistrationTransactor(pool *pgxpool.Pool, virtualProjectionEnabled bool) *RegistrationTransactor {
	return &RegistrationTransactor{pool: pool, virtualProjectionEnabled: virtualProjectionEnabled}
}

func (t *RegistrationTransactor) WithinTransaction(ctx context.Context, run func(applicationregistration.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("registration_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	idempotency := NewIdempotencyRepository(tx)
	outbox := NewOutboxAppender(tx)
	audit := NewAuditAppender(tx)
	repositories := applicationregistration.TxRepositories{
		Registrations: NewRegistrationRepository(tx),
		Challenges: NewChallengeRepository(tx, ChallengeOptions{
			VirtualProjectionEnabled: t.virtualProjectionEnabled,
		}),
		Identities:    NewIdentityRepository(tx),
		Idempotency:   idempotency,
		Intents:       NewIntentRepository(tx),
		UserAuthState: NewUserAuthStateRepository(tx),
		Outbox:        outbox,
		Audit:         audit,
		Session:       NewSessionTxRepositories(tx),
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("registration_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var (
	_ applicationregistration.Repository              = (*RegistrationRepository)(nil)
	_ applicationregistration.ChallengeRepository     = (*ChallengeRepository)(nil)
	_ applicationregistration.IdentityRepository      = (*IdentityRepository)(nil)
	_ applicationregistration.IdempotencyRepository   = (*IdempotencyRepository)(nil)
	_ applicationregistration.IntentRepository        = (*IntentRepository)(nil)
	_ applicationregistration.UserAuthStateRepository = (*UserAuthStateRepository)(nil)
	_ applicationregistration.OutboxAppender          = (*OutboxAppender)(nil)
	_ applicationregistration.AuditAppender           = (*AuditAppender)(nil)
	_ applicationregistration.Transactor              = (*RegistrationTransactor)(nil)
)
