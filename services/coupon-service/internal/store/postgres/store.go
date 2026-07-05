package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/services/coupon-service/internal/model"
	"github.com/Medikong/services/services/coupon-service/internal/repository"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := database.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	return database.RunMigrations(ctx, s.db, migrations)
}

func (s *Store) UpsertPolicy(ctx context.Context, input repository.PolicyInput) (model.Policy, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO coupon_policies (policy_id, drop_id, name, total_quantity, issued_count, status)
		VALUES ($1, $2, $3, $4, 0, $5)
		ON CONFLICT (policy_id) DO UPDATE
		SET drop_id = EXCLUDED.drop_id,
		    name = EXCLUDED.name,
		    total_quantity = EXCLUDED.total_quantity,
		    status = EXCLUDED.status,
		    updated_at = now()`,
		input.PolicyID, input.DropID, input.Name, input.TotalQuantity, input.Status)
	if err != nil {
		return model.Policy{}, err
	}
	return s.GetPolicy(ctx, input.PolicyID)
}

func (s *Store) GetPolicy(ctx context.Context, policyID string) (model.Policy, error) {
	row := s.db.QueryRowContext(ctx, `SELECT policy_id, drop_id, name, total_quantity, issued_count, status FROM coupon_policies WHERE policy_id = $1`, policyID)
	var policy model.Policy
	if err := row.Scan(&policy.PolicyID, &policy.DropID, &policy.Name, &policy.TotalQuantity, &policy.IssuedCount, &policy.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Policy{}, repository.ErrPolicyNotFound
		}
		return model.Policy{}, err
	}
	return policy, nil
}

func (s *Store) Issue(ctx context.Context, policyID string, userID string, idempotencyKey string) (model.IssueResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return model.IssueResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if idempotencyKey != "" {
		if coupon, ok, err := findByIdempotencyKey(ctx, tx, policyID, userID, idempotencyKey); err != nil {
			return model.IssueResult{}, err
		} else if ok {
			if err := tx.Commit(); err != nil {
				return model.IssueResult{}, err
			}
			return model.IssueResult{Result: "duplicate", Coupon: coupon}, nil
		}
	}

	var policy model.Policy
	row := tx.QueryRowContext(ctx, `SELECT policy_id, drop_id, name, total_quantity, issued_count, status FROM coupon_policies WHERE policy_id = $1 FOR UPDATE`, policyID)
	if err := row.Scan(&policy.PolicyID, &policy.DropID, &policy.Name, &policy.TotalQuantity, &policy.IssuedCount, &policy.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.IssueResult{}, repository.ErrPolicyNotFound
		}
		return model.IssueResult{}, err
	}
	if policy.Status != "ready" {
		return model.IssueResult{}, repository.ErrPolicyNotReady
	}

	if coupon, ok, err := findIssuedCoupon(ctx, tx, policyID, userID); err != nil {
		return model.IssueResult{}, err
	} else if ok {
		if idempotencyKey != "" {
			if err := insertIdempotencyKey(ctx, tx, policyID, userID, idempotencyKey, coupon.CouponID); err != nil {
				return model.IssueResult{}, err
			}
		}
		if err := tx.Commit(); err != nil {
			return model.IssueResult{}, err
		}
		return model.IssueResult{Result: "duplicate", Coupon: coupon}, nil
	}

	if policy.IssuedCount >= policy.TotalQuantity {
		return model.IssueResult{}, repository.ErrSoldOut
	}

	coupon := model.Coupon{
		CouponID: newID("coupon"),
		PolicyID: policy.PolicyID,
		DropID:   policy.DropID,
		UserID:   userID,
		Status:   "issued",
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO coupon_issuances (coupon_id, policy_id, drop_id, user_id, status)
		VALUES ($1, $2, $3, $4, $5)`, coupon.CouponID, coupon.PolicyID, coupon.DropID, coupon.UserID, coupon.Status); err != nil {
		if isUniqueViolation(err) {
			existing, ok, findErr := findIssuedCoupon(ctx, tx, policyID, userID)
			if findErr != nil {
				return model.IssueResult{}, findErr
			}
			if ok {
				if err := tx.Commit(); err != nil {
					return model.IssueResult{}, err
				}
				return model.IssueResult{Result: "duplicate", Coupon: existing}, nil
			}
		}
		return model.IssueResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE coupon_policies SET issued_count = issued_count + 1, updated_at = now() WHERE policy_id = $1`, policyID); err != nil {
		return model.IssueResult{}, err
	}
	if idempotencyKey != "" {
		if err := insertIdempotencyKey(ctx, tx, policyID, userID, idempotencyKey, coupon.CouponID); err != nil {
			return model.IssueResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.IssueResult{}, err
	}
	return model.IssueResult{Result: "issued", Coupon: coupon}, nil
}

func (s *Store) ListByUser(ctx context.Context, userID string) ([]model.Coupon, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT coupon_id, policy_id, drop_id, user_id, status FROM coupon_issuances WHERE user_id = $1 ORDER BY issued_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var coupons []model.Coupon
	for rows.Next() {
		var coupon model.Coupon
		if err := rows.Scan(&coupon.CouponID, &coupon.PolicyID, &coupon.DropID, &coupon.UserID, &coupon.Status); err != nil {
			return nil, err
		}
		coupons = append(coupons, coupon)
	}
	return coupons, rows.Err()
}

func findByIdempotencyKey(ctx context.Context, tx *sql.Tx, policyID string, userID string, key string) (model.Coupon, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT i.coupon_id, i.policy_id, i.drop_id, i.user_id, i.status
		FROM coupon_idempotency_keys k
		JOIN coupon_issuances i ON i.coupon_id = k.coupon_id
		WHERE k.policy_id = $1 AND k.user_id = $2 AND k.idempotency_key = $3`, policyID, userID, key)
	var coupon model.Coupon
	if err := row.Scan(&coupon.CouponID, &coupon.PolicyID, &coupon.DropID, &coupon.UserID, &coupon.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Coupon{}, false, nil
		}
		return model.Coupon{}, false, err
	}
	return coupon, true, nil
}

func findIssuedCoupon(ctx context.Context, tx *sql.Tx, policyID string, userID string) (model.Coupon, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT coupon_id, policy_id, drop_id, user_id, status FROM coupon_issuances WHERE policy_id = $1 AND user_id = $2`, policyID, userID)
	var coupon model.Coupon
	if err := row.Scan(&coupon.CouponID, &coupon.PolicyID, &coupon.DropID, &coupon.UserID, &coupon.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Coupon{}, false, nil
		}
		return model.Coupon{}, false, err
	}
	return coupon, true, nil
}

func insertIdempotencyKey(ctx context.Context, tx *sql.Tx, policyID string, userID string, key string, couponID string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO coupon_idempotency_keys (policy_id, user_id, idempotency_key, coupon_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, policyID, userID, key, couponID)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func newID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto random failed: %v", err))
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS coupon_policies (
		policy_id TEXT PRIMARY KEY,
		drop_id TEXT NOT NULL,
		name TEXT NOT NULL,
		total_quantity INTEGER NOT NULL CHECK (total_quantity > 0),
		issued_count INTEGER NOT NULL DEFAULT 0 CHECK (issued_count >= 0),
		status TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS coupon_issuances (
		coupon_id TEXT PRIMARY KEY,
		policy_id TEXT NOT NULL REFERENCES coupon_policies(policy_id),
		drop_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		status TEXT NOT NULL,
		issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		UNIQUE (policy_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS coupon_idempotency_keys (
		policy_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		coupon_id TEXT NOT NULL REFERENCES coupon_issuances(coupon_id),
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (policy_id, user_id, idempotency_key)
	)`,
}
