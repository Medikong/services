package drop

import (
	"context"

	"github.com/Medikong/services/packages/go-platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func OpenPostgresRepository(ctx context.Context, config database.PostgresConfig) (*PostgresRepository, error) {
	db, err := database.OpenPostgres(ctx, config)
	if err != nil {
		return nil, err
	}
	store := NewPostgresRepository(db)
	if err := store.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresRepository) Migrate(ctx context.Context) error {
	return database.RunMigrations(ctx, s.db, migrations)
}

func (s *PostgresRepository) PrepareLocal(ctx context.Context, input PrepareDropInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO products (product_id, name)
		VALUES ($1, $2)
		ON CONFLICT (product_id) DO UPDATE SET name = EXCLUDED.name`, input.ProductID, input.ProductName); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO drops (drop_id, product_id, sale_starts_at, status, coupon_policy_id)
		VALUES ($1, $2, $3, 'prepared_local', $4)
		ON CONFLICT (drop_id) DO UPDATE
		SET product_id = EXCLUDED.product_id,
		    sale_starts_at = EXCLUDED.sale_starts_at,
		    coupon_policy_id = EXCLUDED.coupon_policy_id,
		    status = 'prepared_local'`,
		input.DropID, input.ProductID, input.SaleStartsAt, input.CouponPolicy.PolicyID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventories (drop_id, stock_quantity)
		VALUES ($1, $2)
		ON CONFLICT (drop_id) DO UPDATE SET stock_quantity = EXCLUDED.stock_quantity`,
		input.DropID, input.StockQuantity); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresRepository) MarkCouponPrepared(ctx context.Context, dropID string, policyID string) error {
	_, err := s.db.Exec(ctx, `UPDATE drops SET status = 'ready', coupon_policy_id = $2 WHERE drop_id = $1`, dropID, policyID)
	return err
}

func (s *PostgresRepository) Readiness(ctx context.Context, dropID string) (Readiness, error) {
	readiness := Readiness{
		DropID: dropID,
		Checks: map[string]Check{
			"product":   {Ready: false, Reason: "product not prepared"},
			"drop":      {Ready: false, Reason: "drop not prepared"},
			"inventory": {Ready: false, Reason: "inventory not prepared"},
			"coupon":    {Ready: false, Reason: "coupon policy not prepared"},
		},
	}
	row := s.db.QueryRow(ctx, `
		SELECT p.product_id IS NOT NULL, d.drop_id IS NOT NULL, i.stock_quantity > 0, d.status = 'ready'
		FROM drops d
		LEFT JOIN products p ON p.product_id = d.product_id
		LEFT JOIN inventories i ON i.drop_id = d.drop_id
		WHERE d.drop_id = $1`, dropID)
	var productReady, dropReady, inventoryReady, couponReady bool
	if err := row.Scan(&productReady, &dropReady, &inventoryReady, &couponReady); err != nil {
		if err == pgx.ErrNoRows {
			return readiness, nil
		}
		return Readiness{}, err
	}
	readiness.Checks["product"] = check(productReady, "product not prepared")
	readiness.Checks["drop"] = check(dropReady, "drop not prepared")
	readiness.Checks["inventory"] = check(inventoryReady, "inventory not prepared")
	readiness.Checks["coupon"] = check(couponReady, "coupon policy not prepared")
	readiness.Ready = productReady && dropReady && inventoryReady && couponReady
	return readiness, nil
}

func check(ok bool, reason string) Check {
	if ok {
		return Check{Ready: true}
	}
	return Check{Ready: false, Reason: reason}
}

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS products (
		product_id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS drops (
		drop_id TEXT PRIMARY KEY,
		product_id TEXT NOT NULL REFERENCES products(product_id),
		sale_starts_at TEXT NOT NULL,
		status TEXT NOT NULL,
		coupon_policy_id TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE TABLE IF NOT EXISTS inventories (
		drop_id TEXT PRIMARY KEY REFERENCES drops(drop_id),
		stock_quantity INTEGER NOT NULL CHECK (stock_quantity > 0),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}
