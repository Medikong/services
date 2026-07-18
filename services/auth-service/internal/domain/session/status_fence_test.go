package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type racingStatusCache struct {
	mu     sync.Mutex
	record StatusRecord
	getErr error
}

func (c *racingStatusCache) Get(context.Context, uuid.UUID) (StatusRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.record, c.getErr
}

func (c *racingStatusCache) Put(_ context.Context, record StatusRecord, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record = record
	c.getErr = nil
	return nil
}

func (c *racingStatusCache) PutActiveIfWritable(_ context.Context, record StatusRecord, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.record.State == StatusRevoking {
		return false, nil
	}
	c.record = record
	c.getErr = nil
	return true, nil
}

func (c *racingStatusCache) RestoreActive(_ context.Context, record StatusRecord, fenceVersion int64, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.record.State != StatusRevoking || c.record.Version != fenceVersion {
		return false, nil
	}
	c.record = record
	c.getErr = nil
	return true, nil
}

func (c *racingStatusCache) fence(record StatusRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record = record
	c.getErr = nil
}

func (c *racingStatusCache) state() StatusState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.record.State
}

func Test_StatusService_Check_does_not_repopulate_active_when_fence_wins_cache_fill_race(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &racingStatusCache{getErr: ErrStatusCacheMiss}
	entered, release := make(chan struct{}), make(chan struct{})
	source := &blockingStatusSource{record: fixture.record(StatusActive), entered: entered, release: release}
	service := fixture.service(cache, source)
	result := make(chan StatusState, 1)
	go func() { result <- service.Check(context.Background(), fixture.check()) }()
	<-entered
	cache.fence(fixture.record(StatusRevoking))

	// When
	close(release)
	state := <-result

	// Then
	require.Equal(t, StatusUnavailable, state)
	require.Equal(t, StatusRevoking, cache.state())
}

func Test_StatusService_Check_keeps_fence_when_database_transaction_is_still_active(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{record: fixture.record(StatusRevoking)}
	source := &recordingStatusSource{record: fixture.record(StatusActive)}
	service := fixture.service(cache, source)

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusUnavailable, state)
	require.Equal(t, 1, source.calls)
	require.Equal(t, StatusRevoking, cache.record.State)
}

func Test_StatusService_Check_repairs_fence_from_committed_revocation(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{record: fixture.record(StatusRevoking)}
	revoked := fixture.record(StatusRevoked)
	revokedUntil := fixture.now.Add(15 * time.Minute)
	revoked.RevokedUntil = &revokedUntil
	source := &recordingStatusSource{record: revoked}
	service := fixture.service(cache, source)

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusRevoked, state)
	require.Equal(t, 1, source.calls)
	require.Equal(t, StatusRevoked, cache.record.State)
}
