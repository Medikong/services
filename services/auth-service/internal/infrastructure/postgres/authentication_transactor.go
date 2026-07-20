package postgres

import (
	"context"

	applicationauthentication "github.com/Medikong/services/services/auth-service/internal/application/authentication"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type AuthenticationTransactor struct {
	pool                     *pgxpool.Pool
	virtualProjectionEnabled bool
}

func NewAuthenticationTransactor(pool *pgxpool.Pool, virtualProjectionEnabled bool) *AuthenticationTransactor {
	return &AuthenticationTransactor{pool: pool, virtualProjectionEnabled: virtualProjectionEnabled}
}

func (t *AuthenticationTransactor) WithinTransaction(ctx context.Context, run func(applicationauthentication.TxRepositories) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return oops.In("authentication_transactor").Code("transaction.begin_failed").Wrap(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	outbox := NewOutboxAppender(tx)
	audit := NewAuditAppender(tx)
	repositories := applicationauthentication.TxRepositories{
		Intents:    NewIntentRepository(tx),
		Identities: NewIdentityRepository(tx),
		Challenges: NewChallengeRepository(tx, ChallengeOptions{VirtualProjectionEnabled: t.virtualProjectionEnabled}),
		Session: applicationsession.TxRepositories{
			Sessions:      NewSessionTxRepository(tx),
			UserAuthState: NewSessionUserAuthStateReader(tx),
			Idempotency:   NewIdempotencyRepository(tx),
			Outbox:        outbox,
			Audit:         audit,
		},
		Outbox: outbox,
		Audit:  audit,
	}
	if err := run(repositories); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("authentication_transactor").Code("transaction.commit_failed").Wrap(err)
	}
	return nil
}

var _ applicationauthentication.Transactor = (*AuthenticationTransactor)(nil)
var _ applicationauthentication.IntentRepository = (*IntentRepository)(nil)
var _ applicationauthentication.IdentityRepository = (*IdentityRepository)(nil)
var _ applicationauthentication.ChallengeRepository = (*ChallengeRepository)(nil)
