//go:build integration

package reliability_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestIdempotencyExpiryPreservesBusinessKeyAndAllowsSameRequestResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_idempotency"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, migration.Migrate(ctx, pool))

	now := time.Now().UTC().Truncate(time.Microsecond)
	first := idempotencyCommand("reuse-completed", "request-v1", now)
	claimAndComplete(t, ctx, pool, first, "result-v1")

	replayed := claim(t, ctx, pool, first)
	require.True(t, replayed.Existing)
	require.Equal(t, "completed", replayed.Status)
	require.Equal(t, "result-v1", replayed.ResultRef)

	conflict := idempotencyCommand(first.BusinessKey, "request-conflict", now)
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = reliability.Claim(ctx, tx, conflict, "TestAggregate", "owner-conflict")
	require.Error(t, err)
	require.NoError(t, tx.Rollback(ctx))

	_, err = pool.Exec(ctx, `
		UPDATE coupon_idempotency_records
		SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour'
		WHERE operation_type=$1 AND business_key=$2
	`, first.OperationType, first.BusinessKey)
	require.NoError(t, err)
	second := idempotencyCommand(first.BusinessKey, "request-v2", now)
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	_, err = reliability.Claim(ctx, tx, second, "TestAggregate", "owner-v2")
	require.Error(t, err)
	require.NoError(t, tx.Rollback(ctx))
	replayed = claim(t, ctx, pool, first)
	require.True(t, replayed.Existing)
	require.Equal(t, "result-v1", replayed.ResultRef)

	liveHash := sha256.Sum256([]byte("live-request"))
	_, err = pool.Exec(ctx, `
		INSERT INTO coupon_idempotency_records (
			operation_type,business_key,owner_type,owner_id,request_hash,status,
			locked_until,expires_at,created_at,updated_at
		) VALUES ('test.active','live-processing','TestAggregate','live-owner',$1,'processing',
			now()+interval '1 hour',now()-interval '1 hour',now()-interval '2 hours',now())
	`, liveHash[:])
	require.NoError(t, err)
	live := reliability.Command{
		OperationType: "test.active", BusinessKey: "live-processing", RequestHash: liveHash,
		LeaseUntil: now.Add(2 * time.Hour), ExpiresAt: now.Add(3 * time.Hour),
	}
	replayed = claim(t, ctx, pool, live)
	require.True(t, replayed.Existing)
	require.False(t, replayed.Resume)
	require.Equal(t, "processing", replayed.Status)
	var liveOwner string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT owner_id FROM coupon_idempotency_records
		WHERE operation_type='test.active' AND business_key='live-processing'
	`).Scan(&liveOwner))
	require.Equal(t, "live-owner", liveOwner)

	staleHash := sha256.Sum256([]byte("stale-request"))
	_, err = pool.Exec(ctx, `
		INSERT INTO coupon_idempotency_records (
			operation_type,business_key,owner_type,owner_id,request_hash,status,
			locked_until,expires_at,created_at,updated_at
		) VALUES ('test.stale','stale-processing','TestAggregate','stale-owner',$1,'processing',
			now()-interval '30 minutes',now()-interval '1 hour',now()-interval '2 hours',now())
	`, staleHash[:])
	require.NoError(t, err)
	replacement := reliability.Command{
		OperationType: "test.stale", BusinessKey: "stale-processing", RequestHash: staleHash,
		LeaseUntil: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour),
	}
	tx, err = pool.Begin(ctx)
	require.NoError(t, err)
	replayed, err = reliability.Claim(ctx, tx, replacement, "TestAggregate", "owner:replacement-result")
	require.NoError(t, err)
	require.True(t, replayed.Existing)
	require.True(t, replayed.Resume)
	require.NoError(t, reliability.Complete(ctx, tx, replacement, "replacement-result", map[string]string{"result": "replacement-result"}))
	require.NoError(t, tx.Commit(ctx))
	assertSingleCurrentClaim(t, ctx, pool, replacement, "replacement-result")
}

func claimAndComplete(t *testing.T, ctx context.Context, pool *pgxpool.Pool, command reliability.Command, resultRef string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	replay, err := reliability.Claim(ctx, tx, command, "TestAggregate", "owner:"+resultRef)
	require.NoError(t, err)
	require.False(t, replay.Existing)
	require.NoError(t, reliability.Complete(ctx, tx, command, resultRef, map[string]string{"result": resultRef}))
	require.NoError(t, tx.Commit(ctx))
}

func claim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, command reliability.Command) reliability.Replay {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	replay, err := reliability.Claim(ctx, tx, command, "TestAggregate", "claim-reader")
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return replay
}

func assertSingleCurrentClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, command reliability.Command, resultRef string) {
	t.Helper()
	var count int
	var storedHash []byte
	var storedResult string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*)
		FROM coupon_idempotency_records
		WHERE operation_type=$1 AND business_key=$2
	`, command.OperationType, command.BusinessKey).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT request_hash,result_ref
		FROM coupon_idempotency_records
		WHERE operation_type=$1 AND business_key=$2
	`, command.OperationType, command.BusinessKey).Scan(&storedHash, &storedResult))
	require.Equal(t, command.RequestHash[:], storedHash)
	require.Equal(t, resultRef, storedResult)
}

func idempotencyCommand(businessKey, request string, now time.Time) reliability.Command {
	return idempotencyCommandForOperation("test.completed", businessKey, request, now)
}

func idempotencyCommandForOperation(operation, businessKey, request string, now time.Time) reliability.Command {
	return reliability.Command{
		OperationType: operation, BusinessKey: businessKey, RequestHash: sha256.Sum256([]byte(request)),
		CorrelationID: "corr:" + businessKey, LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(time.Hour),
	}
}
