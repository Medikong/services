//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/services/auth-service/internal/auth"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChallengeRepositoryReissueConsumeAndVirtualProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	repository := challenge.NewPostgresRepository(db, challenge.PostgresOptions{VirtualProjectionEnabled: true})
	now := time.Now().UTC().Truncate(time.Microsecond)
	subjectID := uuid.New()

	first := newChallenge(t, uuid.New(), subjectID, now)
	tx := beginDomainTx(t, ctx, db)
	if err := repository.Issue(ctx, tx, first); err != nil {
		t.Fatalf("issue first challenge: %v", err)
	}
	if err := repository.StoreVirtualProjection(ctx, tx, readyProjection(first, now)); err != nil {
		t.Fatalf("store first virtual projection: %v", err)
	}
	commitDomainTx(t, ctx, tx)

	assertChallengeRowLock(t, ctx, db, repository, first.ID)

	second := newChallenge(t, uuid.New(), subjectID, now.Add(time.Minute))
	tx = beginDomainTx(t, ctx, db)
	if err := repository.Issue(ctx, tx, second); err != nil {
		t.Fatalf("reissue challenge: %v", err)
	}
	if err := repository.StoreVirtualProjection(ctx, tx, readyProjection(second, now.Add(time.Minute))); err != nil {
		t.Fatalf("store second virtual projection: %v", err)
	}
	commitDomainTx(t, ctx, tx)

	var (
		firstStatus            string
		firstCiphertextCleared bool
		firstKeyIDCleared      bool
	)
	if err := db.QueryRow(ctx, `
		SELECT c.status, v.code_ciphertext IS NULL, v.code_key_id IS NULL
		FROM auth_challenges c
		JOIN auth_virtual_verification_messages v ON v.challenge_id = c.challenge_id
		WHERE c.challenge_id = $1
	`, first.ID).Scan(&firstStatus, &firstCiphertextCleared, &firstKeyIDCleared); err != nil {
		t.Fatalf("read revoked first challenge: %v", err)
	}
	if firstStatus != string(challenge.StatusRevoked) || !firstCiphertextCleared || !firstKeyIDCleared {
		t.Fatalf("first challenge was not atomically revoked and shredded: status=%q ciphertext_cleared=%t key_id_cleared=%t", firstStatus, firstCiphertextCleared, firstKeyIDCleared)
	}

	tx = beginDomainTx(t, ctx, db)
	consumed, result, err := repository.Consume(ctx, tx, second.ID, now.Add(2*time.Minute), func(challenge.Challenge) bool { return false })
	if err != nil {
		t.Fatalf("consume mismatch: %v", err)
	}
	if !result.Changed || result.Failure != challenge.ConsumeFailureMismatch || consumed.Status != challenge.StatusIssued || consumed.AttemptCount != 1 {
		t.Fatalf("mismatch result=%#v challenge=%#v", result, consumed)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	consumed, result, err = repository.Consume(ctx, tx, second.ID, now.Add(3*time.Minute), func(challenge.Challenge) bool { return true })
	if err != nil {
		t.Fatalf("consume matching code: %v", err)
	}
	if !result.Verified || !result.Changed || consumed.Status != challenge.StatusVerified || consumed.ConsumedAt == nil {
		t.Fatalf("matching result=%#v challenge=%#v", result, consumed)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	_, err = repository.FindVirtualProjection(ctx, tx, second.ID, now.Add(4*time.Minute))
	if !errors.Is(err, challenge.ErrVirtualUnavailable) {
		t.Fatalf("terminal challenge virtual lookup error=%v", err)
	}
	rollbackDomainTx(ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	_, result, err = repository.Consume(ctx, tx, second.ID, now.Add(5*time.Minute), func(challenge.Challenge) bool { return false })
	if err != nil {
		t.Fatalf("replay verified challenge: %v", err)
	}
	if !result.Verified || !result.AlreadyVerified || result.Changed || result.Failure != challenge.ConsumeFailureNone {
		t.Fatalf("replay result=%#v", result)
	}
	rollbackDomainTx(ctx, tx)
}

func TestRegistrationRepositoryLocksAndRejectsStaleSave(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	repository := registration.NewPostgresRepository(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	registrationID := uuid.New()
	intentID := uuid.New()
	emailIdentityID := uuid.New()
	phoneIdentityID := uuid.New()

	tx := beginDomainTx(t, ctx, db)
	seedRegistrationDependencies(t, ctx, tx, intentID, emailIdentityID, phoneIdentityID, now)
	value, err := registration.New(registration.NewInput{
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
	if err := repository.Create(ctx, tx, value); err != nil {
		t.Fatalf("create registration: %v", err)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	locked, err := repository.FindForUpdate(ctx, tx, registrationID)
	if err != nil {
		t.Fatalf("lock registration: %v", err)
	}

	blockedTx := beginDomainTx(t, ctx, db)
	if _, err := blockedTx.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
		t.Fatalf("set lock timeout: %v", err)
	}
	if _, err := repository.FindForUpdate(ctx, blockedTx, registrationID); err == nil {
		t.Fatal("concurrent registration lock unexpectedly succeeded")
	}
	rollbackDomainTx(ctx, blockedTx)

	stale := value
	if err := locked.AttachChallenge(registration.MethodEmail, uuid.New()); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	if err := repository.Save(ctx, tx, &locked); err != nil {
		t.Fatalf("save locked registration: %v", err)
	}
	if locked.Version != 1 {
		t.Fatalf("registration version after save=%d, want 1", locked.Version)
	}
	commitDomainTx(t, ctx, tx)

	tx = beginDomainTx(t, ctx, db)
	if err := repository.Save(ctx, tx, &stale); !errors.Is(err, registration.ErrVersionConflict) {
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
	if err := auth.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate auth schema: %v", err)
	}
	if err := auth.MigrateDevelopment(ctx, db); err != nil {
		t.Fatalf("migrate development auth schema: %v", err)
	}
	return db
}

func assertChallengeRowLock(t *testing.T, ctx context.Context, db *pgxpool.Pool, repository *challenge.PostgresRepository, challengeID uuid.UUID) {
	t.Helper()
	firstTx := beginDomainTx(t, ctx, db)
	defer rollbackDomainTx(ctx, firstTx)
	if _, err := repository.FindForUpdate(ctx, firstTx, challengeID); err != nil {
		t.Fatalf("lock challenge: %v", err)
	}

	secondTx := beginDomainTx(t, ctx, db)
	defer rollbackDomainTx(ctx, secondTx)
	if _, err := secondTx.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
		t.Fatalf("set challenge lock timeout: %v", err)
	}
	if _, err := repository.FindForUpdate(ctx, secondTx, challengeID); err == nil {
		t.Fatal("concurrent challenge lock unexpectedly succeeded")
	}
}

func newChallenge(t *testing.T, id, subjectID uuid.UUID, now time.Time) challenge.Challenge {
	t.Helper()
	value, err := challenge.New(challenge.NewInput{
		ID:                 id,
		SubjectType:        challenge.SubjectRegistration,
		SubjectID:          subjectID,
		Purpose:            challenge.PurposeSignupEmail,
		Method:             challenge.MethodEmail,
		Channel:            challenge.ChannelEmailCode,
		Destination:        "masked@example.test",
		CodeHash:           hash32(byte(now.UnixNano())),
		VerifierKeyVersion: 1,
		MaxAttempts:        3,
		MaxSends:           3,
		NextSendAt:         now,
		ExpiresAt:          now.Add(10 * time.Minute),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	return value
}

func readyProjection(value challenge.Challenge, now time.Time) challenge.VirtualProjection {
	return challenge.VirtualProjection{
		ChallengeID:       value.ID,
		Channel:           value.Channel,
		ChallengeVersion:  value.Version,
		CodeCiphertext:    []byte("test-encrypted-code"),
		CodeKeyID:         "test-virtual-key",
		MaskedDestination: "m***@example.test",
		Status:            challenge.VirtualReady,
		ExpiresAt:         value.ExpiresAt,
		CreatedAt:         now,
	}
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
