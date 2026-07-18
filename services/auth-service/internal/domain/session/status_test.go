package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type recordingStatusCache struct {
	record   StatusRecord
	getErr   error
	putErr   error
	putCalls int
	putTTL   time.Duration
}

func (c *recordingStatusCache) Get(context.Context, uuid.UUID) (StatusRecord, error) {
	return c.record, c.getErr
}

func (c *recordingStatusCache) Put(_ context.Context, record StatusRecord, ttl time.Duration) error {
	c.record = record
	c.putTTL = ttl
	c.putCalls++
	return c.putErr
}

func (c *recordingStatusCache) PutActiveIfWritable(ctx context.Context, record StatusRecord, ttl time.Duration) (bool, error) {
	if c.record.State == StatusRevoking {
		return false, nil
	}
	return true, c.Put(ctx, record, ttl)
}

func (c *recordingStatusCache) RestoreActive(ctx context.Context, record StatusRecord, fenceVersion int64, ttl time.Duration) (bool, error) {
	if c.record.State != StatusRevoking || c.record.Version != fenceVersion {
		return false, nil
	}
	return true, c.Put(ctx, record, ttl)
}

type recordingStatusSource struct {
	record StatusRecord
	err    error
	calls  int
}

func (s *recordingStatusSource) FindStatus(context.Context, uuid.UUID) (StatusRecord, error) {
	s.calls++
	return s.record, s.err
}

type blockingStatusSource struct {
	record  StatusRecord
	entered chan<- struct{}
	release <-chan struct{}
}

func (s *blockingStatusSource) FindStatus(context.Context, uuid.UUID) (StatusRecord, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.record, nil
}

func Test_StatusService_Check_returns_active_from_cache_without_database_lookup(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{record: fixture.record(StatusActive)}
	source := &recordingStatusSource{err: errors.New("database must not be called")}
	service := fixture.service(cache, source)

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusActive, state)
	require.Zero(t, source.calls)
}

func Test_StatusService_Check_returns_revoked_from_cache_without_database_lookup(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	record := fixture.record(StatusRevoked)
	revokedUntil := fixture.now.Add(10 * time.Minute)
	record.RevokedUntil = &revokedUntil
	cache := &recordingStatusCache{record: record}
	source := &recordingStatusSource{err: errors.New("database must not be called")}
	service := fixture.service(cache, source)

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusRevoked, state)
	require.Zero(t, source.calls)
}

func Test_StatusService_Check_fills_cache_from_database_on_cache_miss(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{getErr: ErrStatusCacheMiss}
	source := &recordingStatusSource{record: fixture.record(StatusActive)}
	service := fixture.service(cache, source)

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusActive, state)
	require.Equal(t, 1, source.calls)
	require.Equal(t, 1, cache.putCalls)
	require.Positive(t, cache.putTTL)
	require.LessOrEqual(t, cache.putTTL, 5*time.Minute)
}

func Test_StatusService_Check_caps_active_cache_ttl_at_session_remaining_time(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	record := fixture.record(StatusActive)
	record.AbsoluteExpiresAt = fixture.now.Add(45 * time.Second)
	cache := &recordingStatusCache{getErr: ErrStatusCacheMiss}
	service := fixture.service(cache, &recordingStatusSource{record: record})

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusActive, state)
	require.Equal(t, 45*time.Second, cache.putTTL)
}

func Test_StatusService_Check_returns_expired_for_stale_cached_active_record(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	record := fixture.record(StatusActive)
	record.AbsoluteExpiresAt = fixture.now.Add(-time.Second)
	service := fixture.service(&recordingStatusCache{record: record}, &recordingStatusSource{})

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusExpired, state)
}

func Test_StatusService_Check_returns_unavailable_when_redis_fails(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{getErr: errors.New("redis unavailable")}
	source := &recordingStatusSource{record: fixture.record(StatusActive)}
	service := fixture.service(cache, source)

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusUnavailable, state)
	require.Zero(t, source.calls)
}

func Test_StatusService_Check_returns_unavailable_when_database_fallback_fails(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{getErr: ErrStatusCacheMiss}
	service := fixture.service(cache, &recordingStatusSource{err: errors.New("database unavailable")})

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusUnavailable, state)
	require.Zero(t, cache.putCalls)
}

func Test_StatusService_Check_returns_unavailable_when_cache_fill_fails(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{getErr: ErrStatusCacheMiss, putErr: errors.New("redis write failed")}
	service := fixture.service(cache, &recordingStatusSource{record: fixture.record(StatusActive)})

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusUnavailable, state)
}

func Test_StatusService_Check_returns_unavailable_when_fallback_budget_is_full(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	entered, release := make(chan struct{}), make(chan struct{})
	source := &blockingStatusSource{record: fixture.record(StatusActive), entered: entered, release: release}
	service := NewStatusService(StatusServiceOptions{
		Cache: &recordingStatusCache{getErr: ErrStatusCacheMiss}, Source: source,
		Now:    func() time.Time { return fixture.now },
		Config: StatusServiceConfig{ActiveTTL: 5 * time.Minute, AccessTTL: 15 * time.Minute, FallbackTimeout: time.Second, MaxFallbacks: 1},
	})
	firstResult := make(chan StatusState, 1)
	go func() { firstResult <- service.Check(context.Background(), fixture.check()) }()
	<-entered

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusUnavailable, state)
	close(release)
	require.Equal(t, StatusActive, <-firstResult)
}

func Test_StatusService_Check_does_not_project_token_identifier(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	cache := &recordingStatusCache{getErr: ErrStatusCacheMiss}
	service := fixture.service(cache, &recordingStatusSource{record: fixture.record(StatusActive)})

	// When
	state := service.Check(context.Background(), fixture.check())

	// Then
	require.Equal(t, StatusActive, state)
	require.NotContains(t, cache.record.RedisFields(), fixture.tokenID.String())
}

func Test_StatusService_Project_writes_revoked_state_through_until_access_expiry(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	record := fixture.record(StatusRevoked)
	revokedUntil := fixture.now.Add(15 * time.Minute)
	record.RevokedUntil = &revokedUntil
	cache := &recordingStatusCache{}
	service := fixture.service(cache, &recordingStatusSource{record: record})

	// When
	err := service.Project(context.Background(), fixture.sessionID)

	// Then
	require.NoError(t, err)
	require.Equal(t, StatusRevoked, cache.record.State)
	require.Equal(t, 15*time.Minute, cache.putTTL)
}

func Test_StatusService_Project_returns_error_when_revocation_cache_write_fails(t *testing.T) {
	// Given
	fixture := newStatusFixture()
	record := fixture.record(StatusRevoked)
	cache := &recordingStatusCache{putErr: errors.New("redis write failed")}
	service := fixture.service(cache, &recordingStatusSource{record: record})

	// When
	err := service.Project(context.Background(), fixture.sessionID)

	// Then
	require.Error(t, err)
}

type statusFixture struct {
	now       time.Time
	userID    uuid.UUID
	sessionID uuid.UUID
	tokenID   uuid.UUID
}

func newStatusFixture() statusFixture {
	return statusFixture{
		now:       time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
		userID:    uuid.New(),
		sessionID: uuid.New(),
		tokenID:   uuid.New(),
	}
}

func (f statusFixture) record(state StatusState) StatusRecord {
	idleExpiry := f.now.Add(20 * time.Minute)
	return StatusRecord{
		UserID: f.userID, SessionID: f.sessionID, State: state,
		IdleExpiresAt: &idleExpiry, AbsoluteExpiresAt: f.now.Add(time.Hour), Version: 3,
	}
}

func (f statusFixture) check() StatusCheck {
	return StatusCheck{UserID: f.userID, SessionID: f.sessionID, TokenID: f.tokenID}
}

func (f statusFixture) service(cache StatusCache, source StatusSource) *StatusService {
	return NewStatusService(StatusServiceOptions{
		Cache: cache, Source: source, Now: func() time.Time { return f.now },
		Config: StatusServiceConfig{ActiveTTL: 5 * time.Minute, AccessTTL: 15 * time.Minute, FallbackTimeout: 100 * time.Millisecond, MaxFallbacks: 32},
	})
}
