//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	authmigration "github.com/Medikong/services/services/auth-service/internal/infrastructure/migration"
	storage "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestChallengeRepositoryReissueConsumeAndVirtualProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedChallengePool(t, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	subjectID := uuid.New()

	first := newTestChallenge(t, uuid.New(), subjectID, now)
	tx, repository := beginChallengeTx(t, ctx, db, true)
	if err := repository.Issue(ctx, first); err != nil {
		t.Fatalf("issue first challenge: %v", err)
	}
	if err := repository.StoreVirtualProjection(ctx, readyChallengeProjection(first, now)); err != nil {
		t.Fatalf("store first virtual projection: %v", err)
	}
	commitChallengeTx(t, ctx, tx)

	assertChallengeRowLock(t, ctx, db, first.ID)

	second := newTestChallenge(t, uuid.New(), subjectID, now.Add(time.Minute))
	tx, repository = beginChallengeTx(t, ctx, db, true)
	if err := repository.Issue(ctx, second); err != nil {
		t.Fatalf("reissue challenge: %v", err)
	}
	if err := repository.StoreVirtualProjection(ctx, readyChallengeProjection(second, now.Add(time.Minute))); err != nil {
		t.Fatalf("store second virtual projection: %v", err)
	}
	commitChallengeTx(t, ctx, tx)

	var firstStatus string
	var ciphertextCleared, keyIDCleared bool
	if err := db.QueryRow(ctx, `
		SELECT c.status, v.code_ciphertext IS NULL, v.code_key_id IS NULL
		FROM auth_challenges c
		JOIN auth_virtual_verification_messages v ON v.challenge_id = c.challenge_id
		WHERE c.challenge_id = $1
	`, first.ID).Scan(&firstStatus, &ciphertextCleared, &keyIDCleared); err != nil {
		t.Fatalf("read revoked first challenge: %v", err)
	}
	if firstStatus != string(domainchallenge.StatusRevoked) || !ciphertextCleared || !keyIDCleared {
		t.Fatal("reissued challenge did not atomically revoke and destroy the previous projection")
	}

	tx, repository = beginChallengeTx(t, ctx, db, true)
	consumed, result, err := domainchallenge.Consume(ctx, repository, second.ID, now.Add(2*time.Minute), func(domainchallenge.Challenge) bool { return false })
	if err != nil {
		t.Fatalf("consume mismatch: %v", err)
	}
	if !result.Changed || result.Failure != domainchallenge.ConsumeFailureMismatch || consumed.Status != domainchallenge.StatusIssued || consumed.AttemptCount != 1 {
		t.Fatal("challenge mismatch result does not match the expected state")
	}
	commitChallengeTx(t, ctx, tx)

	tx, repository = beginChallengeTx(t, ctx, db, true)
	consumed, result, err = domainchallenge.Consume(ctx, repository, second.ID, now.Add(3*time.Minute), func(domainchallenge.Challenge) bool { return true })
	if err != nil {
		t.Fatalf("consume matching code: %v", err)
	}
	if !result.Verified || !result.Changed || consumed.Status != domainchallenge.StatusVerified || consumed.ConsumedAt == nil {
		t.Fatal("challenge verification result does not match the expected state")
	}
	commitChallengeTx(t, ctx, tx)

	tx, repository = beginChallengeTx(t, ctx, db, true)
	_, err = repository.FindVirtualProjection(ctx, second.ID, now.Add(4*time.Minute))
	if !errors.Is(err, domainchallenge.ErrVirtualUnavailable) {
		t.Fatalf("terminal challenge virtual lookup error=%v", err)
	}
	rollbackChallengeTx(ctx, tx)

	tx, repository = beginChallengeTx(t, ctx, db, true)
	_, result, err = domainchallenge.Consume(ctx, repository, second.ID, now.Add(5*time.Minute), func(domainchallenge.Challenge) bool { return false })
	if err != nil {
		t.Fatalf("replay verified challenge: %v", err)
	}
	if !result.Verified || !result.AlreadyVerified || result.Changed || result.Failure != domainchallenge.ConsumeFailureNone {
		t.Fatal("verified challenge replay was not idempotent")
	}
	rollbackChallengeTx(ctx, tx)
}

func TestChallengeDeliveryRepositoryClaimsAndDestroysPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedChallengePool(t, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	value := newTestChallenge(t, uuid.New(), uuid.New(), now)
	payloadID := uuid.New()
	tx, repository := beginChallengeTx(t, ctx, db, false)
	if err := repository.Issue(ctx, value); err != nil {
		t.Fatalf("issue delivery challenge: %v", err)
	}
	if err := repository.StoreDeliveryPayload(ctx, domainchallenge.DeliveryPayload{
		ID: payloadID, ChallengeID: value.ID, SendSequence: 1,
		Ciphertext: []byte("encrypted-payload"), KeyID: "test-key", AADHash: challengeHash32(9), ExpiresAt: value.ExpiresAt,
	}); err != nil {
		t.Fatalf("store delivery payload: %v", err)
	}
	commitChallengeTx(t, ctx, tx)

	deliveries := storage.NewChallengeDeliveryRepository(db)
	claimed, err := deliveries.Claim(ctx, "test-worker", 1, time.Minute)
	if err != nil {
		t.Fatalf("claim delivery: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != payloadID || claimed[0].Attempts != 1 || claimed[0].Channel != value.Channel {
		t.Fatal("claimed delivery does not match the stored payload")
	}
	if err := deliveries.MarkDelivered(ctx, payloadID, "test-worker", "provider-request"); err != nil {
		t.Fatalf("mark delivery complete: %v", err)
	}
	var status string
	var ciphertextCleared, keyIDCleared, aadCleared bool
	if err := db.QueryRow(ctx, `
		SELECT delivery_status, payload_ciphertext IS NULL, payload_key_id IS NULL, aad_hash IS NULL
		FROM auth_verification_delivery_payloads WHERE delivery_payload_id = $1
	`, payloadID).Scan(&status, &ciphertextCleared, &keyIDCleared, &aadCleared); err != nil {
		t.Fatalf("read delivered payload: %v", err)
	}
	if status != "delivered" || !ciphertextCleared || !keyIDCleared || !aadCleared {
		t.Fatal("delivered payload was not destroyed")
	}
}

func assertChallengeRowLock(t *testing.T, ctx context.Context, db *pgxpool.Pool, challengeID uuid.UUID) {
	t.Helper()
	firstTx, first := beginChallengeTx(t, ctx, db, true)
	defer rollbackChallengeTx(ctx, firstTx)
	if _, err := first.FindForUpdate(ctx, challengeID); err != nil {
		t.Fatalf("lock challenge: %v", err)
	}

	secondTx, second := beginChallengeTx(t, ctx, db, true)
	defer rollbackChallengeTx(ctx, secondTx)
	if _, err := secondTx.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
		t.Fatalf("set challenge lock timeout: %v", err)
	}
	if _, err := second.FindForUpdate(ctx, challengeID); err == nil {
		t.Fatal("concurrent challenge lock unexpectedly succeeded")
	}
}

func migratedChallengePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
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

func beginChallengeTx(t *testing.T, ctx context.Context, db *pgxpool.Pool, virtual bool) (pgx.Tx, *storage.ChallengeRepository) {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx, storage.NewChallengeRepository(tx, storage.ChallengeOptions{VirtualProjectionEnabled: virtual})
}

func commitChallengeTx(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
}

func rollbackChallengeTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}

func newTestChallenge(t *testing.T, id, subjectID uuid.UUID, now time.Time) domainchallenge.Challenge {
	t.Helper()
	value, err := domainchallenge.New(domainchallenge.NewInput{
		ID: id, SubjectType: domainchallenge.SubjectRegistration, SubjectID: subjectID,
		Purpose: domainchallenge.PurposeSignupEmail, Method: domainchallenge.MethodEmail,
		Channel: domainchallenge.ChannelEmailCode, Destination: "masked-destination",
		CodeHash: challengeHash32(byte(now.UnixNano())), VerifierKeyVersion: 1,
		MaxAttempts: 3, MaxSends: 3, NextSendAt: now,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	return value
}

func readyChallengeProjection(value domainchallenge.Challenge, now time.Time) domainchallenge.VirtualProjection {
	return domainchallenge.VirtualProjection{
		ChallengeID: value.ID, Channel: value.Channel, ChallengeVersion: value.Version,
		CodeCiphertext: []byte("encrypted-code"), CodeKeyID: "test-virtual-key",
		MaskedDestination: "masked-destination", Status: domainchallenge.VirtualReady,
		ExpiresAt: value.ExpiresAt, CreatedAt: now,
	}
}

func challengeHash32(seed byte) []byte {
	value := make([]byte, 32)
	for index := range value {
		value[index] = seed
	}
	return value
}
