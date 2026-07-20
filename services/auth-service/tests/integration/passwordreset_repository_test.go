//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	applicationpasswordreset "github.com/Medikong/services/services/auth-service/internal/application/passwordreset"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	"github.com/google/uuid"
)

func TestPasswordResetTransactionRollbackAndOptimisticConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	rolledBackID := uuid.New()
	rolledBack, err := domainpasswordreset.New(rolledBackID, nil, nil, now.Add(15*time.Minute), now)
	if err != nil {
		t.Fatalf("new rollback reset: %v", err)
	}
	rollbackMarker := errors.New("force rollback")
	err = postgresinfra.NewPasswordResetTransactor(db, false).WithinTransaction(ctx, func(repositories applicationpasswordreset.TxRepositories) error {
		if createErr := repositories.Resets.Create(ctx, rolledBack); createErr != nil {
			return createErr
		}
		return rollbackMarker
	})
	if !errors.Is(err, rollbackMarker) {
		t.Fatalf("transaction error=%v, want rollback marker", err)
	}
	var rolledBackCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_password_resets WHERE password_reset_id = $1`, rolledBackID).Scan(&rolledBackCount); err != nil {
		t.Fatalf("count rolled back reset: %v", err)
	}
	if rolledBackCount != 0 {
		t.Fatalf("rolled back reset count=%d, want 0", rolledBackCount)
	}

	resetID := uuid.New()
	created, err := domainpasswordreset.New(resetID, nil, nil, now.Add(15*time.Minute), now)
	if err != nil {
		t.Fatalf("new committed reset: %v", err)
	}
	if err := postgresinfra.NewPasswordResetTransactor(db, false).WithinTransaction(ctx, func(repositories applicationpasswordreset.TxRepositories) error {
		return repositories.Resets.Create(ctx, created)
	}); err != nil {
		t.Fatalf("commit reset: %v", err)
	}

	tx := beginDomainTx(t, ctx, db)
	repository := postgresinfra.NewPasswordResetRepository(tx)
	locked, err := repository.FindForUpdate(ctx, resetID)
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("find reset for update: %v", err)
	}
	stale := locked
	locked.ExpiresAt = locked.ExpiresAt.Add(time.Minute)
	if err := repository.Save(ctx, &locked); err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("save locked reset: %v", err)
	}
	if locked.Version != 1 {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("reset version=%d, want 1", locked.Version)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	if err := postgresinfra.NewPasswordResetRepository(tx).Save(ctx, &stale); !errors.Is(err, domainpasswordreset.ErrVersionConflict) {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("stale reset save error=%v, want %v", err, domainpasswordreset.ErrVersionConflict)
	}
	rollbackDomainTx(ctx, tx)
}
