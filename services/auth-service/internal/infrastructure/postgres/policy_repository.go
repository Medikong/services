package postgres

import (
	"context"
	"encoding/json"
	"errors"

	domainpolicy "github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PolicyRepository struct {
	tx pgx.Tx
}

func NewPolicyRepository(tx pgx.Tx) *PolicyRepository {
	return &PolicyRepository{tx: tx}
}

func (r *PolicyRepository) ListActive(ctx context.Context) ([]domainpolicy.Snapshot, error) {
	return r.scanSnapshots(ctx, `
		SELECT policy_name, policy_version, status, rules, effective_at
		FROM auth_policies
		WHERE status = 'active'
		ORDER BY policy_name
	`)
}

func (r *PolicyRepository) ListActiveForUpdate(ctx context.Context) ([]domainpolicy.Snapshot, error) {
	return r.scanSnapshots(ctx, `
		SELECT policy_name, policy_version, status, rules, effective_at
		FROM auth_policies
		WHERE status = 'active'
		ORDER BY policy_name
		FOR UPDATE
	`)
}

func (r *PolicyRepository) scanSnapshots(ctx context.Context, query string) ([]domainpolicy.Snapshot, error) {
	rows, err := r.tx.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domainpolicy.Snapshot
	for rows.Next() {
		var snapshot domainpolicy.Snapshot
		if err := rows.Scan(&snapshot.Name, &snapshot.Version, &snapshot.Status, &snapshot.Rules, &snapshot.EffectiveAt); err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

func (r *PolicyRepository) FindActiveForUpdate(ctx context.Context, name string) (domainpolicy.Snapshot, error) {
	var snapshot domainpolicy.Snapshot
	err := r.tx.QueryRow(ctx, `
		SELECT policy_name, policy_version, status, rules, effective_at
		FROM auth_policies
		WHERE policy_name = $1 AND status = 'active'
		FOR UPDATE
	`, name).Scan(&snapshot.Name, &snapshot.Version, &snapshot.Status, &snapshot.Rules, &snapshot.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainpolicy.Snapshot{}, domainpolicy.ErrNotFound
	}
	return snapshot, err
}

func (r *PolicyRepository) SupersedeAndInsert(ctx context.Context, previous domainpolicy.Snapshot, rules json.RawMessage, changeReason string, operatorID uuid.UUID) (domainpolicy.Snapshot, error) {
	_, err := r.tx.Exec(ctx, `
		UPDATE auth_policies
		SET status = 'superseded', superseded_at = now(), updated_at = now()
		WHERE policy_version = $1
	`, previous.Version)
	if err != nil {
		return domainpolicy.Snapshot{}, err
	}
	var result domainpolicy.Snapshot
	err = r.tx.QueryRow(ctx, `
		INSERT INTO auth_policies (
			policy_name, status, rules, activation_source,
			activated_by_user_id, change_reason, effective_at
		) VALUES ($1, 'active', $2, 'operator', $3, $4, now())
		RETURNING policy_name, policy_version, status, rules, effective_at
	`, previous.Name, rules, operatorID, changeReason).Scan(
		&result.Name, &result.Version, &result.Status, &result.Rules, &result.EffectiveAt,
	)
	return result, err
}

func (r *PolicyRepository) FindGlobalActive(ctx context.Context) (domainpolicy.GlobalSnapshot, error) {
	return r.findGlobalSnapshot(ctx, false)
}

func (r *PolicyRepository) FindGlobalActiveForUpdate(ctx context.Context) (domainpolicy.GlobalSnapshot, error) {
	return r.findGlobalSnapshot(ctx, true)
}

func (r *PolicyRepository) findGlobalSnapshot(ctx context.Context, forUpdate bool) (domainpolicy.GlobalSnapshot, error) {
	query := `
		SELECT policy_snapshot_version, status, document, effective_at
		FROM auth_policy_global_snapshots
		WHERE status = 'active'
	`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var snapshot domainpolicy.GlobalSnapshot
	err := r.tx.QueryRow(ctx, query).Scan(&snapshot.Version, &snapshot.Status, &snapshot.Document, &snapshot.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domainpolicy.GlobalSnapshot{}, domainpolicy.ErrNotFound
	}
	return snapshot, err
}

func (r *PolicyRepository) ActivateGlobal(ctx context.Context, document json.RawMessage, operatorID uuid.UUID, changeReason string) (domainpolicy.GlobalSnapshot, error) {
	if _, err := r.tx.Exec(ctx, `
		UPDATE auth_policy_global_snapshots
		SET status = 'superseded', superseded_at = now(), updated_at = now()
		WHERE status = 'active'
	`); err != nil {
		return domainpolicy.GlobalSnapshot{}, err
	}
	var snapshot domainpolicy.GlobalSnapshot
	err := r.tx.QueryRow(ctx, `
		INSERT INTO auth_policy_global_snapshots (
			status, document, activation_source, activated_by_user_id,
			change_reason, effective_at
		) VALUES ('active', $1, 'operator', $2, $3, now())
		RETURNING policy_snapshot_version, status, document, effective_at
	`, document, operatorID, changeReason).Scan(
		&snapshot.Version, &snapshot.Status, &snapshot.Document, &snapshot.EffectiveAt,
	)
	return snapshot, err
}

var _ domainpolicy.Repository = (*PolicyRepository)(nil)
