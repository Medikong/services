package migration

import (
	"context"
	"testing"
)

func TestMigrationRequiresPostgresPool(t *testing.T) {
	if err := Migrate(context.Background(), nil); err == nil {
		t.Fatal("Migrate() error = nil without a PostgreSQL pool")
	}
	if err := CheckSchema(context.Background(), nil); err == nil {
		t.Fatal("CheckSchema() error = nil without a PostgreSQL pool")
	}
}
