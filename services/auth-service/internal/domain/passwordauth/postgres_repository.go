package passwordauth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlreadyExists      = errors.New("auth credential already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
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

func (r PostgresRepository) CreatePassword(ctx context.Context, credential PasswordCredential) error {
	_, err := r.exec(ctx, `
		INSERT INTO auth_credentials (auth_account_id, email, password_hash)
		VALUES ($1, $2, $3)`,
		credential.AuthAccountID, credential.Email, credential.PasswordHash)
	if isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	return err
}

func (r PostgresRepository) FindPasswordByEmail(ctx context.Context, email string) (PasswordCredential, error) {
	row := r.queryRow(ctx, `
		SELECT credential_id, auth_account_id, email, password_hash, created_at, updated_at
		FROM auth_credentials
		WHERE email = $1`, email)
	var credential PasswordCredential
	if err := row.Scan(
		&credential.CredentialID,
		&credential.AuthAccountID,
		&credential.Email,
		&credential.PasswordHash,
		&credential.CreatedAt,
		&credential.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PasswordCredential{}, ErrInvalidCredentials
		}
		return PasswordCredential{}, err
	}
	return credential, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r PostgresRepository) exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if r.tx != nil {
		return r.tx.Exec(ctx, sql, args...)
	}
	return r.pool.Exec(ctx, sql, args...)
}

func (r PostgresRepository) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if r.tx != nil {
		return r.tx.QueryRow(ctx, sql, args...)
	}
	return r.pool.QueryRow(ctx, sql, args...)
}

var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS auth_credentials (
			credential_id BIGSERIAL PRIMARY KEY,
			auth_account_id TEXT NOT NULL REFERENCES auth_accounts(auth_account_id),
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	`ALTER TABLE auth_credentials ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
}
