//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	redisinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/redis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func Test_SessionStatusProjection_real_Redis_rejects_stale_terminal_version(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	options, err := redis.ParseURL(startRedis(t, ctx))
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	userID, sessionID := seedProjectionSession(t, ctx, db)
	projection, err := redisinfra.NewSessionProjection(
		postgresinfra.NewSessionRepository(db), client,
		time.Second, 100*time.Millisecond, 5*time.Minute, 15*time.Minute, 32,
	)
	require.NoError(t, err)
	validUntil := time.Now().UTC().Add(time.Hour)
	newer := domainsession.StatusChange{
		SessionID: sessionID, UserID: userID, Status: domainsession.StatusRevoked,
		Version: 10, ValidUntil: validUntil, OccurredAt: time.Now().UTC(),
	}
	require.NoError(t, projection.Apply(ctx, newer))

	stale := newer
	stale.Status = domainsession.StatusReuseDetected
	stale.Version = 9
	require.NoError(t, projection.Apply(ctx, stale))
	assertCachedProjection(t, ctx, client, sessionID.String(), domainsession.StatusRevoked, 10)

	latest := stale
	latest.Version = 11
	require.NoError(t, projection.Apply(ctx, latest))
	assertCachedProjection(t, ctx, client, sessionID.String(), domainsession.StatusReuseDetected, 11)
	allowed, err := projection.Check(ctx, userID, sessionID)
	require.NoError(t, err)
	require.False(t, allowed)
}

func assertCachedProjection(t *testing.T, ctx context.Context, client *redis.Client, sessionID, wantStatus string, wantVersion int64) {
	t.Helper()
	encoded, err := client.Get(ctx, "auth:session-status:v2:"+sessionID).Bytes()
	require.NoError(t, err)
	var cached struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	}
	require.NoError(t, json.Unmarshal(encoded, &cached))
	require.Equal(t, wantStatus, cached.Status)
	require.Equal(t, wantVersion, cached.Version)
}
