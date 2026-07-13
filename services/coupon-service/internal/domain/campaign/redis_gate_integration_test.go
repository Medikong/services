//go:build integration

package campaign_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
)

func TestRedisGateRequestTransitionsAreAtomic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp"),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	address, err := container.Endpoint(ctx, "")
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	gate, err := campaign.NewRedisGate(client, "coupon", time.Minute)
	require.NoError(t, err)

	admitted, err := gate.Admit(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1, 2)
	require.NoError(t, err)
	require.Equal(t, campaign.GateAdmitted, admitted.Signal)
	require.EqualValues(t, 1, admitted.Remaining)
	replayed, err := gate.Admit(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1, 2)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)

	rejected, err := gate.Admit(ctx, "camp_abcdefgh", "ireq_ijklmnop", 2, 2)
	require.NoError(t, err)
	require.True(t, rejected.Rejected())
	compensated, err := gate.Compensate(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1)
	require.NoError(t, err)
	require.Equal(t, campaign.GateCompensated, compensated.Signal)
	require.EqualValues(t, 2, compensated.Remaining)

	admitted, err = gate.Admit(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1, 2)
	require.NoError(t, err)
	completed, err := gate.Complete(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1)
	require.NoError(t, err)
	require.Equal(t, campaign.GateCompleted, completed.Signal)
	completed, err = gate.Complete(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1)
	require.NoError(t, err)
	require.True(t, completed.Replayed)
	_, err = gate.Compensate(ctx, "camp_abcdefgh", "ireq_abcdefgh", 1)
	require.ErrorIs(t, err, campaign.ErrGateStateConflict)

	const attempts = 20
	results := make(chan campaign.GateResult, attempts)
	errors := make(chan error, attempts)
	var group sync.WaitGroup
	for i := 0; i < attempts; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			result, err := gate.Admit(ctx, "camp_concurrent", "ireq_"+strconv.Itoa(10000000+index), 1, 5)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(i)
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var acceptedCount int
	for result := range results {
		if !result.Rejected() {
			acceptedCount++
		}
	}
	require.Equal(t, 5, acceptedCount)
}
