//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	authmigration "github.com/Medikong/services/services/auth-service/internal/infrastructure/migration"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistrationRepositoryLocksAndRejectsStaleSave(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	registrationID := uuid.New()
	intentID := uuid.New()
	emailIdentityID := uuid.New()
	phoneIdentityID := uuid.New()

	tx := beginDomainTx(t, ctx, db)
	repository := postgresinfra.NewRegistrationRepository(tx)
	seedRegistrationDependencies(t, ctx, tx, intentID, emailIdentityID, phoneIdentityID, now)
	value, err := domainregistration.New(domainregistration.NewInput{
		ID:                 registrationID,
		IntentID:           intentID,
		EmailIdentityID:    emailIdentityID,
		PhoneIdentityID:    phoneIdentityID,
		ProfileRequestID:   "profile-request",
		AgreementReceiptID: "agreement-receipt",
		ClientChannel:      "web",
		StatusTokenHash:    hash32(31),
		StatusTokenKeyVer:  1,
		StatusTokenExpires: now.Add(2 * time.Hour),
		ExpiresAt:          now.Add(time.Hour),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("new registration: %v", err)
	}
	if err := repository.Create(ctx, value); err != nil {
		t.Fatalf("create registration: %v", err)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	repository = postgresinfra.NewRegistrationRepository(tx)
	locked, err := repository.FindForUpdate(ctx, registrationID)
	if err != nil {
		t.Fatalf("lock registration: %v", err)
	}

	blockedTx := beginDomainTx(t, ctx, db)
	if _, err := blockedTx.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
		t.Fatalf("set lock timeout: %v", err)
	}
	if _, err := postgresinfra.NewRegistrationRepository(blockedTx).FindForUpdate(ctx, registrationID); err == nil {
		t.Fatal("concurrent registration lock unexpectedly succeeded")
	}
	rollbackDomainTx(ctx, blockedTx)

	stale := value
	if err := locked.AttachChallenge(domainregistration.MethodEmail, uuid.New()); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	if err := repository.Save(ctx, &locked); err != nil {
		t.Fatalf("save locked registration: %v", err)
	}
	if locked.Version != 1 {
		t.Fatalf("registration version after save=%d, want 1", locked.Version)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	repository = postgresinfra.NewRegistrationRepository(tx)
	if err := repository.Save(ctx, &stale); !errors.Is(err, domainregistration.ErrVersionConflict) {
		t.Fatalf("stale registration save error=%v", err)
	}
	rollbackDomainTx(ctx, tx)
}

func migratedDomainPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := startPostgres(t, ctx)
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}
	t.Cleanup(db.Close)
	if err := audit.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate audit schema: %v", err)
	}
	if err := authmigration.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate auth schema: %v", err)
	}
	if err := authmigration.MigrateDevelopment(ctx, db); err != nil {
		t.Fatalf("migrate development auth schema: %v", err)
	}
	return db
}

func seedRegistrationDependencies(t *testing.T, ctx context.Context, tx pgx.Tx, intentID, emailIdentityID, phoneIdentityID uuid.UUID, now time.Time) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_authentication_intents (
			intent_id, client_channel, return_path, intent_type,
			owner_proof_hash, csrf_secret_hash, csrf_key_version, expires_at
		) VALUES ($1, 'web', '/', 'authenticate', $2, $3, 1, $4)
	`, intentID, hash32(21), hash32(22), now.Add(2*time.Hour)); err != nil {
		t.Fatalf("seed authentication intent: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_identities (identity_id, identity_type, identity_namespace, normalized_value)
		VALUES ($1, 'email', 'default', $2), ($3, 'phone', 'default', $4)
	`, emailIdentityID, "email-"+emailIdentityID.String(), phoneIdentityID, "phone-"+phoneIdentityID.String()); err != nil {
		t.Fatalf("seed identities: %v", err)
	}
}

func beginDomainTx(t *testing.T, ctx context.Context, db *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx
}

func commitDomainTx(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
}

func rollbackDomainTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}

func hash32(seed byte) []byte {
	value := make([]byte, 32)
	for index := range value {
		value[index] = seed
	}
	return value
}
