package redis

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

var projectionTestNow = time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)

func TestSessionProjectionCachesActiveStatusWithBoundedTTL(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	repository := newProjectionRepository(domainsession.Session{
		ID: sessionID, UserID: userID, Status: "active", Version: 7,
		ExpiresAt: projectionTestNow.Add(time.Hour),
	})
	projection, redisServer, client := newTestProjection(t, repository, 5*time.Minute, 20*time.Minute)

	allowed, err := projection.Check(context.Background(), userID, sessionID)
	if err != nil || !allowed {
		t.Fatalf("Check() = (%v, %v), want (true, nil)", allowed, err)
	}
	assertCachedStatus(t, client, sessionID, "active", 7)
	assertPTTL(t, client, sessionStatusKeyPrefix+sessionID.String(), 5*time.Minute)
	if member, err := client.SIsMember(context.Background(), userSessionsKeyPrefix+userID.String(), sessionID.String()).Result(); err != nil || !member {
		t.Fatalf("reverse index membership = (%v, %v), want (true, nil)", member, err)
	}

	allowed, err = projection.Check(context.Background(), userID, sessionID)
	if err != nil || !allowed || repository.callCount() != 1 {
		t.Fatalf("cached Check() = (%v, %v), repository calls = %d", allowed, err, repository.callCount())
	}

	redisServer.FastForward(5*time.Minute + time.Millisecond)
	if exists := client.Exists(context.Background(), sessionStatusKeyPrefix+sessionID.String()).Val(); exists != 0 {
		t.Fatalf("active cache exists after TTL: %d", exists)
	}
}

func TestSessionProjectionCachesDatabaseTerminalStatus(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	repository := newProjectionRepository(domainsession.Session{
		ID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 3,
		ExpiresAt: projectionTestNow.Add(time.Hour),
	})
	projection, _, client := newTestProjection(t, repository, 5*time.Minute, 20*time.Minute)

	allowed, err := projection.Check(context.Background(), userID, sessionID)
	if err != nil || allowed {
		t.Fatalf("Check() = (%v, %v), want (false, nil)", allowed, err)
	}
	assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, 3)
	assertPTTL(t, client, sessionStatusKeyPrefix+sessionID.String(), 20*time.Minute)

	allowed, err = projection.Check(context.Background(), userID, sessionID)
	if err != nil || allowed || repository.callCount() != 1 {
		t.Fatalf("tombstone Check() = (%v, %v), repository calls = %d", allowed, err, repository.callCount())
	}
}

func TestSessionProjectionTerminalCASRejectsLateActiveWrite(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	stale := domainsession.Session{
		ID: sessionID, UserID: userID, Status: "active", Version: 1,
		ExpiresAt: projectionTestNow.Add(time.Hour),
	}
	projection, _, client := newTestProjection(t, newProjectionRepository(stale), 5*time.Minute, 20*time.Minute)
	change := domainsession.StatusChange{
		SessionID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 2,
		ValidUntil: stale.ExpiresAt, OccurredAt: projectionTestNow,
	}
	if err := projection.Apply(context.Background(), change); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	allowed, err := projection.writeActive(context.Background(), stale)
	if err != nil || allowed {
		t.Fatalf("late writeActive() = (%v, %v), want (false, nil)", allowed, err)
	}
	assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, 2)
	if member := client.SIsMember(context.Background(), userSessionsKeyPrefix+userID.String(), sessionID.String()).Val(); member {
		t.Fatal("late active write restored the reverse index")
	}
}

func TestSessionProjectionCheckRejectsTerminalWrittenDuringDatabaseFallback(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	stale := domainsession.Session{
		ID: sessionID, UserID: userID, Status: "active", Version: 1,
		ExpiresAt: projectionTestNow.Add(time.Hour),
	}
	repository := &blockingProjectionRepository{
		current: stale, entered: make(chan struct{}), release: make(chan struct{}),
	}
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	projection, err := NewSessionProjection(repository, client, time.Second, 500*time.Millisecond, 5*time.Minute, 20*time.Minute)
	if err != nil {
		t.Fatalf("NewSessionProjection() error = %v", err)
	}
	projection.now = func() time.Time { return projectionTestNow }

	type checkResult struct {
		allowed bool
		err     error
	}
	result := make(chan checkResult, 1)
	go func() {
		allowed, checkErr := projection.Check(context.Background(), userID, sessionID)
		result <- checkResult{allowed: allowed, err: checkErr}
	}()
	select {
	case <-repository.entered:
	case <-time.After(time.Second):
		t.Fatal("database fallback did not start")
	}
	if err := projection.Apply(context.Background(), domainsession.StatusChange{
		SessionID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 2,
		ValidUntil: stale.ExpiresAt, OccurredAt: projectionTestNow,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	close(repository.release)

	select {
	case got := <-result:
		if got.err != nil || got.allowed {
			t.Fatalf("Check() after concurrent terminal = (%v, %v), want (false, nil)", got.allowed, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("database fallback did not finish")
	}
	assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, 2)
}

func TestSessionProjectionCASUsesVersionAndTerminalPrecedence(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	current := domainsession.Session{
		ID: sessionID, UserID: userID, Status: "active", Version: 2,
		ExpiresAt: projectionTestNow.Add(time.Hour),
	}
	projection, _, client := newTestProjection(t, newProjectionRepository(current), 5*time.Minute, 20*time.Minute)
	if allowed, err := projection.writeActive(context.Background(), current); err != nil || !allowed {
		t.Fatalf("writeActive() = (%v, %v)", allowed, err)
	}

	older := domainsession.StatusChange{
		SessionID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 1,
		ValidUntil: current.ExpiresAt, OccurredAt: projectionTestNow,
	}
	if err := projection.Apply(context.Background(), older); err != nil {
		t.Fatalf("Apply(older) error = %v", err)
	}
	assertCachedStatus(t, client, sessionID, "active", 2)
	if member := client.SIsMember(context.Background(), userSessionsKeyPrefix+userID.String(), sessionID.String()).Val(); !member {
		t.Fatal("lower terminal version removed the active reverse index")
	}

	equal := older
	equal.Version = 2
	if err := projection.Apply(context.Background(), equal); err != nil {
		t.Fatalf("Apply(equal) error = %v", err)
	}
	assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, 2)
	if member := client.SIsMember(context.Background(), userSessionsKeyPrefix+userID.String(), sessionID.String()).Val(); member {
		t.Fatal("equal-version terminal did not remove the reverse index")
	}
}

func TestSessionProjectionRevokeSessionWritesExactTombstone(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	active := domainsession.Session{
		ID: sessionID, UserID: userID, Status: "active", Version: 4,
		ExpiresAt: projectionTestNow.Add(time.Hour),
	}
	repository := newProjectionRepository(active)
	projection, _, client := newTestProjection(t, repository, 5*time.Minute, 20*time.Minute)
	if allowed, err := projection.Check(context.Background(), userID, sessionID); err != nil || !allowed {
		t.Fatalf("warm Check() = (%v, %v)", allowed, err)
	}
	repository.store(domainsession.Session{
		ID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 5,
		ExpiresAt: active.ExpiresAt,
	})

	if err := projection.RevokeSession(context.Background(), sessionID); err != nil {
		t.Fatalf("RevokeSession() error = %v", err)
	}
	assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, 5)
	if exists := client.Exists(context.Background(), sessionStatusKeyPrefix+sessionID.String()).Val(); exists != 1 {
		t.Fatalf("tombstone key count = %d, want 1", exists)
	}
}

func TestSessionProjectionRevokeUserWritesTombstones(t *testing.T) {
	userID := uuid.New()
	firstID := uuid.New()
	secondID := uuid.New()
	expiresAt := projectionTestNow.Add(time.Hour)
	repository := newProjectionRepository(
		domainsession.Session{ID: firstID, UserID: userID, Status: "active", Version: 1, ExpiresAt: expiresAt},
		domainsession.Session{ID: secondID, UserID: userID, Status: "active", Version: 1, ExpiresAt: expiresAt},
	)
	projection, _, client := newTestProjection(t, repository, 5*time.Minute, 20*time.Minute)
	for index, sessionID := range []uuid.UUID{firstID, secondID} {
		if allowed, err := projection.Check(context.Background(), userID, sessionID); err != nil || !allowed {
			t.Fatalf("warm Check(%d) = (%v, %v)", index, allowed, err)
		}
		repository.store(domainsession.Session{
			ID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 2, ExpiresAt: expiresAt,
		})
	}

	if err := projection.RevokeUser(context.Background(), userID); err != nil {
		t.Fatalf("RevokeUser() error = %v", err)
	}
	if calls := repository.callCount(); calls != 2 {
		t.Fatalf("RevokeUser() database calls = %d, want 0 additional calls", calls)
	}
	for _, sessionID := range []uuid.UUID{firstID, secondID} {
		assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, conservativeVersion)
	}
	if members := client.SCard(context.Background(), userSessionsKeyPrefix+userID.String()).Val(); members != 0 {
		t.Fatalf("reverse index members = %d, want 0", members)
	}
}

func TestSessionProjectionTerminalApplyHealsCorruptValue(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	expiresAt := projectionTestNow.Add(time.Hour)
	projection, _, client := newTestProjection(t, newProjectionRepository(), 5*time.Minute, 20*time.Minute)
	key := sessionStatusKeyPrefix + sessionID.String()
	if err := client.Set(context.Background(), key, "not-json", time.Hour).Err(); err != nil {
		t.Fatalf("seed corrupt cache: %v", err)
	}
	if _, err := projection.Check(context.Background(), userID, sessionID); err == nil {
		t.Fatal("Check() accepted corrupt cache")
	}

	if err := projection.Apply(context.Background(), domainsession.StatusChange{
		SessionID: sessionID, UserID: userID, Status: domainsession.StatusRevoked, Version: 1,
		ValidUntil: expiresAt, OccurredAt: projectionTestNow,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	assertCachedStatus(t, client, sessionID, domainsession.StatusRevoked, 1)
	if allowed, err := projection.Check(context.Background(), userID, sessionID); err != nil || allowed {
		t.Fatalf("healed Check() = (%v, %v), want (false, nil)", allowed, err)
	}
}

func TestSessionProjectionTombstoneTTLIsBoundedAndNegativeCachesExpired(t *testing.T) {
	userID := uuid.New()
	projection, _, client := newTestProjection(t, newProjectionRepository(), 5*time.Minute, 20*time.Minute)

	shortID := uuid.New()
	if err := projection.Apply(context.Background(), domainsession.StatusChange{
		SessionID: shortID, UserID: userID, Status: domainsession.StatusRevoked, Version: 1,
		ValidUntil: projectionTestNow.Add(2 * time.Minute), OccurredAt: projectionTestNow,
	}); err != nil {
		t.Fatalf("Apply(short) error = %v", err)
	}
	assertPTTL(t, client, sessionStatusKeyPrefix+shortID.String(), 2*time.Minute)

	expiredID := uuid.New()
	if err := projection.Apply(context.Background(), domainsession.StatusChange{
		SessionID: expiredID, UserID: userID, Status: domainsession.StatusRevoked, Version: 1,
		ValidUntil: projectionTestNow.Add(-time.Minute), OccurredAt: projectionTestNow,
	}); err != nil {
		t.Fatalf("Apply(expired) error = %v", err)
	}
	assertPTTL(t, client, sessionStatusKeyPrefix+expiredID.String(), 20*time.Minute)
}

func TestNewSessionProjectionRejectsInvalidTTL(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repository := newProjectionRepository()
	tests := []struct {
		name                 string
		active, tombstoneTTL time.Duration
	}{
		{name: "active missing", tombstoneTTL: time.Minute},
		{name: "tombstone shorter", active: 2 * time.Minute, tombstoneTTL: time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSessionProjection(repository, client, time.Second, 100*time.Millisecond, test.active, test.tombstoneTTL); err == nil {
				t.Fatal("NewSessionProjection() error = nil")
			}
		})
	}
}

func newTestProjection(
	t *testing.T,
	repository *projectionRepository,
	activeTTL, tombstoneTTL time.Duration,
) (*SessionProjection, *miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	projection, err := NewSessionProjection(repository, client, time.Second, 500*time.Millisecond, activeTTL, tombstoneTTL)
	if err != nil {
		t.Fatalf("NewSessionProjection() error = %v", err)
	}
	projection.now = func() time.Time { return projectionTestNow }
	return projection, server, client
}

func assertCachedStatus(t *testing.T, client *goredis.Client, sessionID uuid.UUID, status string, version int64) {
	t.Helper()
	encoded, err := client.Get(context.Background(), sessionStatusKeyPrefix+sessionID.String()).Bytes()
	if err != nil {
		t.Fatalf("read cached status: %v", err)
	}
	var cached sessionStatusValue
	if err := json.Unmarshal(encoded, &cached); err != nil {
		t.Fatalf("decode cached status: %v", err)
	}
	if cached.Status != status || cached.Version != version || cached.SessionID != sessionID.String() {
		t.Fatal("cached status did not match the expected state")
	}
}

func assertPTTL(t *testing.T, client *goredis.Client, key string, want time.Duration) {
	t.Helper()
	got, err := client.PTTL(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("read cache TTL: %v", err)
	}
	const tolerance = 10 * time.Millisecond
	if got < want-tolerance || got > want+tolerance {
		t.Fatalf("cache TTL = %s, want %s", got, want)
	}
}

type projectionRepository struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]domainsession.Session
	err      error
	calls    int
}

func newProjectionRepository(sessions ...domainsession.Session) *projectionRepository {
	repository := &projectionRepository{sessions: make(map[uuid.UUID]domainsession.Session)}
	for _, current := range sessions {
		repository.sessions[current.ID] = current
	}
	return repository
}

func (r *projectionRepository) store(current domainsession.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[current.ID] = current
}

func (r *projectionRepository) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *projectionRepository) FindStatus(_ context.Context, sessionID uuid.UUID) (domainsession.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return domainsession.Session{}, r.err
	}
	current, ok := r.sessions[sessionID]
	if !ok {
		return domainsession.Session{}, domainsession.ErrNotFound
	}
	return current, nil
}

func (*projectionRepository) FindByWebSecret(context.Context, []byte) (domainsession.Session, domainsession.Credential, error) {
	return domainsession.Session{}, domainsession.Credential{}, domainsession.ErrNotFound
}

func (*projectionRepository) FindActive(context.Context, uuid.UUID) (domainsession.Session, error) {
	return domainsession.Session{}, domainsession.ErrNotFound
}

var _ domainsession.Repository = (*projectionRepository)(nil)

type blockingProjectionRepository struct {
	current domainsession.Session
	entered chan struct{}
	release chan struct{}
}

func (r *blockingProjectionRepository) FindStatus(context.Context, uuid.UUID) (domainsession.Session, error) {
	close(r.entered)
	<-r.release
	return r.current, nil
}

func (*blockingProjectionRepository) FindByWebSecret(context.Context, []byte) (domainsession.Session, domainsession.Credential, error) {
	return domainsession.Session{}, domainsession.Credential{}, domainsession.ErrNotFound
}

func (*blockingProjectionRepository) FindActive(context.Context, uuid.UUID) (domainsession.Session, error) {
	return domainsession.Session{}, domainsession.ErrNotFound
}

var _ domainsession.Repository = (*blockingProjectionRepository)(nil)
