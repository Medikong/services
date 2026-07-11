package policy

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("policy not found")

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) ListActive(ctx context.Context) ([]Snapshot, error) {
	return scanSnapshots(ctx, r.pool, `
		SELECT policy_name, policy_version, status, rules, effective_at
		FROM auth_policies WHERE status = 'active' ORDER BY policy_name
	`)
}

func (r *PostgresRepository) ListActiveForUpdate(ctx context.Context, tx pgx.Tx) ([]Snapshot, error) {
	return scanSnapshots(ctx, tx, `
		SELECT policy_name, policy_version, status, rules, effective_at
		FROM auth_policies WHERE status = 'active' ORDER BY policy_name FOR UPDATE
	`)
}

type rowsQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func scanSnapshots(ctx context.Context, queryer rowsQueryer, query string) ([]Snapshot, error) {
	rows, err := queryer.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Snapshot
	for rows.Next() {
		var snapshot Snapshot
		if err := rows.Scan(&snapshot.Name, &snapshot.Version, &snapshot.Status, &snapshot.Rules, &snapshot.EffectiveAt); err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) FindActiveForUpdate(ctx context.Context, tx pgx.Tx, name string) (Snapshot, error) {
	var snapshot Snapshot
	err := tx.QueryRow(ctx, `
		SELECT policy_name, policy_version, status, rules, effective_at
		FROM auth_policies WHERE policy_name = $1 AND status = 'active' FOR UPDATE
	`, name).Scan(&snapshot.Name, &snapshot.Version, &snapshot.Status, &snapshot.Rules, &snapshot.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	return snapshot, err
}

func (r *PostgresRepository) SupersedeAndInsert(ctx context.Context, tx pgx.Tx, previous Snapshot, rules json.RawMessage, changeReason string, operatorID uuid.UUID) (Snapshot, error) {
	_, err := tx.Exec(ctx, `
		UPDATE auth_policies SET status = 'superseded', superseded_at = now(), updated_at = now()
		WHERE policy_version = $1
	`, previous.Version)
	if err != nil {
		return Snapshot{}, err
	}
	var result Snapshot
	err = tx.QueryRow(ctx, `
		INSERT INTO auth_policies (policy_name, status, rules, activation_source, activated_by_user_id, change_reason, effective_at)
		VALUES ($1, 'active', $2, 'operator', $3, $4, now())
		RETURNING policy_name, policy_version, status, rules, effective_at
	`, previous.Name, rules, operatorID, changeReason).Scan(&result.Name, &result.Version, &result.Status, &result.Rules, &result.EffectiveAt)
	return result, err
}

func (r *PostgresRepository) FindGlobalActive(ctx context.Context) (GlobalSnapshot, error) {
	return findGlobalSnapshot(ctx, r.pool, false)
}

func (r *PostgresRepository) FindGlobalActiveForUpdate(ctx context.Context, tx pgx.Tx) (GlobalSnapshot, error) {
	return findGlobalSnapshot(ctx, tx, true)
}

func findGlobalSnapshot(ctx context.Context, q rowQueryer, forUpdate bool) (GlobalSnapshot, error) {
	query := `
		SELECT policy_snapshot_version, status, document, effective_at
		FROM auth_policy_global_snapshots
		WHERE status = 'active'
	`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var snapshot GlobalSnapshot
	err := q.QueryRow(ctx, query).Scan(&snapshot.Version, &snapshot.Status, &snapshot.Document, &snapshot.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GlobalSnapshot{}, ErrNotFound
	}
	return snapshot, err
}

func (r *PostgresRepository) ActivateGlobal(ctx context.Context, tx pgx.Tx, document json.RawMessage, operatorID uuid.UUID, changeReason string) (GlobalSnapshot, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE auth_policy_global_snapshots
		SET status = 'superseded', superseded_at = now(), updated_at = now()
		WHERE status = 'active'
	`); err != nil {
		return GlobalSnapshot{}, err
	}
	var snapshot GlobalSnapshot
	err := tx.QueryRow(ctx, `
		INSERT INTO auth_policy_global_snapshots (
			status, document, activation_source, activated_by_user_id, change_reason, effective_at
		) VALUES ('active', $1, 'operator', $2, $3, now())
		RETURNING policy_snapshot_version, status, document, effective_at
	`, document, operatorID, changeReason).Scan(&snapshot.Version, &snapshot.Status, &snapshot.Document, &snapshot.EffectiveAt)
	return snapshot, err
}
