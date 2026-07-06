package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Medikong/services/packages/go-platform/database"
)

type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type DB struct {
	SQL *sql.DB
}

func Open(ctx context.Context, databaseURL string) (*DB, error) {
	db, err := database.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &DB{SQL: db}, nil
}

func (db *DB) Migrate(ctx context.Context, migrations []string) error {
	return database.RunMigrations(ctx, db.SQL, migrations)
}

func (db *DB) Ping(ctx context.Context) error {
	return db.SQL.PingContext(ctx)
}

func (db *DB) WithTx(ctx context.Context, fn func(Executor) error) (err error) {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	return fn(tx)
}

func MergeMigrations(groups ...[]string) []string {
	var merged []string
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return merged
}

func NewID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, RandomHex(12))
}

func RandomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto random failed: %v", err))
	}
	return hex.EncodeToString(buf)
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func SplitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
