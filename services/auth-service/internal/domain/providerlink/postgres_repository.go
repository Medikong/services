package providerlink

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlreadyExists = errors.New("auth provider link already exists")
	ErrNotFound      = errors.New("auth provider link not found")
)

type PostgresRepository struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

func NewPostgresRepository(pool *pgxpool.Pool) PostgresRepository {
	return PostgresRepository{pool: pool}
}

func NewPostgresTxRepository(tx pgx.Tx) PostgresRepository {
	return PostgresRepository{tx: tx}
}

func (r PostgresRepository) Create(ctx context.Context, link Link) (Link, error) {
	row := r.queryRow(ctx, `
		INSERT INTO auth_provider_links (
			auth_account_id,
			auth_provider,
			provider_subject,
			provider_email,
			provider_email_verified
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING provider_link_id, auth_account_id, auth_provider, provider_subject, provider_email, provider_email_verified, created_at, updated_at`,
		link.AuthAccountID,
		link.AuthProvider,
		link.ProviderSubject,
		link.ProviderEmail,
		link.ProviderEmailVerified,
	)
	created, err := scanLink(row)
	if isUniqueViolation(err) {
		return Link{}, ErrAlreadyExists
	}
	return created, err
}

func (r PostgresRepository) FindByProviderSubject(ctx context.Context, provider string, subject string) (Link, error) {
	row := r.queryRow(ctx, `
		SELECT provider_link_id, auth_account_id, auth_provider, provider_subject, provider_email, provider_email_verified, created_at, updated_at
		FROM auth_provider_links
		WHERE auth_provider = $1 AND provider_subject = $2`,
		provider,
		subject,
	)
	link, err := scanLink(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return link, err
}

type linkScanner interface {
	Scan(dest ...any) error
}

func scanLink(row linkScanner) (Link, error) {
	var link Link
	if err := row.Scan(
		&link.ProviderLinkID,
		&link.AuthAccountID,
		&link.AuthProvider,
		&link.ProviderSubject,
		&link.ProviderEmail,
		&link.ProviderEmailVerified,
		&link.CreatedAt,
		&link.UpdatedAt,
	); err != nil {
		return Link{}, err
	}
	return link, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r PostgresRepository) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if r.tx != nil {
		return r.tx.QueryRow(ctx, sql, args...)
	}
	return r.pool.QueryRow(ctx, sql, args...)
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_provider_links (
		provider_link_id BIGSERIAL PRIMARY KEY,
		auth_account_id TEXT NOT NULL REFERENCES auth_accounts(auth_account_id),
		auth_provider TEXT NOT NULL,
		provider_subject TEXT NOT NULL,
		provider_email TEXT NOT NULL DEFAULT '',
		provider_email_verified BOOLEAN NOT NULL DEFAULT false,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		UNIQUE (auth_provider, provider_subject)
	)`,
	`ALTER TABLE auth_provider_links ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
}
