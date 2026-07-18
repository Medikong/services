package session

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type expiredFallbackSource struct {
	record StatusRecord
}

func (s expiredFallbackSource) FindStatus(ctx context.Context, _ uuid.UUID) (StatusRecord, error) {
	<-ctx.Done()
	return s.record, nil
}

type fallbackContextCache struct {
	putContextErr error
}

func (c *fallbackContextCache) Get(context.Context, uuid.UUID) (StatusRecord, error) {
	return StatusRecord{}, ErrStatusCacheMiss
}

func (c *fallbackContextCache) Put(ctx context.Context, _ StatusRecord, _ time.Duration) error {
	c.putContextErr = ctx.Err()
	return c.putContextErr
}

func (c *fallbackContextCache) PutActiveIfWritable(ctx context.Context, _ StatusRecord, _ time.Duration) (bool, error) {
	c.putContextErr = ctx.Err()
	return c.putContextErr == nil, c.putContextErr
}

func (c *fallbackContextCache) RestoreActive(context.Context, StatusRecord, int64, time.Duration) (bool, error) {
	return false, nil
}

func Test_StatusService_Check_does_not_fill_active_after_fallback_deadline(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &fallbackContextCache{}
	service := NewStatusService(StatusServiceOptions{
		Cache: cache, Source: expiredFallbackSource{record: fixture.record(StatusActive)},
		Now: func() time.Time { return fixture.now },
		Config: StatusServiceConfig{
			ActiveTTL: time.Minute, AccessTTL: 15 * time.Minute,
			FallbackTimeout: time.Millisecond, MaxFallbacks: 1,
		},
	})

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusUnavailable, state)
	require.ErrorIs(t, cache.putContextErr, context.DeadlineExceeded)
}
