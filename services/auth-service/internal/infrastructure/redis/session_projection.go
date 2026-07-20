package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	applicationsessionprojection "github.com/Medikong/services/services/auth-service/internal/application/sessionprojection"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

const (
	sessionStatusKeyPrefix = "auth:session-status:v2:"
	userSessionsKeyPrefix  = "auth:user-sessions:v2:"
	conservativeVersion    = int64(1<<63 - 1)

	cacheResultTerminal      = int64(0)
	cacheResultApplied       = int64(1)
	cacheResultCurrentActive = int64(2)
)

var applyStatusScript = goredis.NewScript(`
local incoming = ARGV[1]
local incoming_version = tonumber(ARGV[2])
local incoming_status = ARGV[3]
local ttl_ms = tonumber(ARGV[4])
local current_raw = redis.call("GET", KEYS[1])

if current_raw then
  local decoded_ok, current = pcall(cjson.decode, current_raw)
  if decoded_ok and type(current) == "table" then
    local current_version = tonumber(current.version)
    local current_status = tostring(current.status or "")
    if current_version then
      if current_version > incoming_version then
        if current_status == "active" then
          return 2
        end
        return 0
      end
      if current_status ~= "active" then
        if incoming_status == "active" then
          return 0
        end
      end
    elseif incoming_status == "active" then
      return redis.error_reply("invalid session status cache version")
    end
  elseif incoming_status == "active" then
    return redis.error_reply("invalid session status cache value")
  end
end

redis.call("PSETEX", KEYS[1], ttl_ms, incoming)
if incoming_status == "active" then
  return 1
end
return 0
`)

type SessionProjection struct {
	repository        domainsession.Repository
	redis             *goredis.Client
	timeout           time.Duration
	dbFallbackTimeout time.Duration
	activeTTL         time.Duration
	tombstoneTTL      time.Duration
	now               func() time.Time
}

type sessionStatusValue struct {
	UserID    string    `json:"userId"`
	SessionID string    `json:"sessionId"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expiresAt"`
	Version   int64     `json:"version"`
}

func NewSessionProjection(
	repository domainsession.Repository,
	client *goredis.Client,
	timeout, dbFallbackTimeout, activeTTL, tombstoneTTL time.Duration,
) (*SessionProjection, error) {
	if repository == nil || client == nil || timeout <= 0 || dbFallbackTimeout <= 0 || dbFallbackTimeout > timeout ||
		activeTTL <= 0 || tombstoneTTL < activeTTL {
		return nil, oops.In("auth_session_status").Code("session_status.invalid_config").New("invalid Session status projection configuration")
	}
	return &SessionProjection{
		repository: repository, redis: client, timeout: timeout, dbFallbackTimeout: dbFallbackTimeout,
		activeTTL: activeTTL, tombstoneTTL: tombstoneTTL, now: time.Now,
	}, nil
}

// Check returns false for a rejected credential and an error for unavailable storage.
func (s *SessionProjection) Check(ctx context.Context, userID, sessionID uuid.UUID) (bool, error) {
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return false, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	key := sessionStatusKeyPrefix + sessionID.String()
	encoded, err := s.redis.Get(checkCtx, key).Bytes()
	if err == nil {
		return s.allowCached(encoded, userID, sessionID)
	}
	if !errors.Is(err, goredis.Nil) {
		return false, oops.In("auth_session_status").Code("session_status.cache_unavailable").Wrap(err)
	}

	dbCtx, cancelDB := context.WithTimeout(checkCtx, s.dbFallbackTimeout)
	current, err := s.repository.FindStatus(dbCtx, sessionID)
	cancelDB()
	if errors.Is(err, domainsession.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, oops.In("auth_session_status").Code("session_status.database_unavailable").Wrap(err)
	}
	if current.UserID != userID {
		return false, nil
	}
	if current.Status != "active" || !s.now().UTC().Before(current.ExpiresAt) {
		if err := s.applyTerminalSession(checkCtx, current); err != nil {
			return false, err
		}
		return false, nil
	}
	return s.writeActive(checkCtx, current)
}

func (s *SessionProjection) allowCached(encoded []byte, userID, sessionID uuid.UUID) (bool, error) {
	var cached sessionStatusValue
	if json.Unmarshal(encoded, &cached) != nil || cached.Version < 0 || cached.Status == "" || cached.SessionID == "" {
		return false, oops.In("auth_session_status").Code("session_status.cache_invalid").New("Session status cache value is invalid")
	}
	return cached.Status == "active" && cached.UserID == userID.String() && cached.SessionID == sessionID.String() &&
		s.now().UTC().Before(cached.ExpiresAt), nil
}

func (s *SessionProjection) writeActive(ctx context.Context, current domainsession.Session) (bool, error) {
	remaining := current.ExpiresAt.Sub(s.now().UTC())
	if remaining <= 0 {
		return false, nil
	}
	ttl := minDuration(remaining, s.activeTTL)
	value := sessionStatusValue{
		UserID: current.UserID.String(), SessionID: current.ID.String(), Status: "active",
		ExpiresAt: current.ExpiresAt.UTC(), Version: current.Version,
	}
	result, err := s.applyValue(ctx, value, ttl)
	if err != nil {
		return false, oops.In("auth_session_status").Code("session_status.write_through_failed").Wrap(err)
	}
	if result == cacheResultTerminal {
		return false, nil
	}
	if err := s.trackActive(ctx, current.UserID, current.ID); err != nil {
		return false, oops.In("auth_session_status").Code("session_status.write_through_failed").Wrap(err)
	}
	return result == cacheResultApplied || result == cacheResultCurrentActive, nil
}

func (s *SessionProjection) Apply(ctx context.Context, change domainsession.StatusChange) error {
	if err := change.Validate(); err != nil {
		return oops.In("auth_session_status").Code("session_status.invalid_change").Wrap(err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	value := sessionStatusValue{
		UserID: change.UserID.String(), SessionID: change.SessionID.String(), Status: change.Status,
		ExpiresAt: change.ValidUntil.UTC(), Version: change.Version,
	}
	result, err := s.applyValue(writeCtx, value, s.terminalTTL(change.ValidUntil))
	if err != nil {
		return oops.In("auth_session_status").Code("session_status.tombstone_failed").Wrap(err)
	}
	if result == cacheResultCurrentActive {
		return nil
	}
	if err := s.untrack(writeCtx, change.UserID, change.SessionID); err != nil {
		return oops.In("auth_session_status").Code("session_status.tombstone_failed").Wrap(err)
	}
	return nil
}

// RevokeSession is the synchronous fast path. The durable projection worker
// applies the transactionally queued status change again when Redis recovers.
func (s *SessionProjection) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	if sessionID == uuid.Nil {
		return oops.In("auth_session_status").Code("session_status.session_required").New("Session ID is required")
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	current, err := s.repository.FindStatus(writeCtx, sessionID)
	if err == nil && current.UserID != uuid.Nil {
		if err := s.applyTerminalSession(writeCtx, current); err != nil {
			return oops.In("auth_session_status").Code("session_status.revoke_failed").Wrap(err)
		}
		return nil
	}
	value := sessionStatusValue{
		SessionID: sessionID.String(), Status: domainsession.StatusRevoked,
		ExpiresAt: s.now().UTC().Add(s.tombstoneTTL), Version: conservativeVersion,
	}
	if _, applyErr := s.applyValue(writeCtx, value, s.tombstoneTTL); applyErr != nil {
		return oops.In("auth_session_status").Code("session_status.revoke_failed").Wrap(errors.Join(err, applyErr))
	}
	return nil
}

// RevokeUser invalidates the sessions visible in the short-lived reverse
// index. The durable PostgreSQL queue covers sessions racing with this lookup.
func (s *SessionProjection) RevokeUser(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return oops.In("auth_session_status").Code("session_status.user_required").New("user ID is required")
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	sessionIDs, err := s.redis.SMembers(writeCtx, userSessionsKeyPrefix+userID.String()).Result()
	if err != nil {
		return oops.In("auth_session_status").Code("session_status.user_lookup_failed").Wrap(err)
	}
	for _, rawSessionID := range sessionIDs {
		sessionID, parseErr := uuid.Parse(rawSessionID)
		if parseErr != nil {
			continue
		}
		value := sessionStatusValue{
			UserID: userID.String(), SessionID: sessionID.String(), Status: domainsession.StatusRevoked,
			ExpiresAt: s.now().UTC().Add(s.tombstoneTTL), Version: conservativeVersion,
		}
		if _, applyErr := s.applyValue(writeCtx, value, s.tombstoneTTL); applyErr != nil {
			return oops.In("auth_session_status").Code("session_status.user_revoke_failed").Wrap(applyErr)
		}
		if untrackErr := s.untrack(writeCtx, userID, sessionID); untrackErr != nil {
			return oops.In("auth_session_status").Code("session_status.user_revoke_failed").Wrap(untrackErr)
		}
	}
	return nil
}

func (s *SessionProjection) applyTerminalSession(ctx context.Context, current domainsession.Session) error {
	status := domainsession.StatusRevoked
	if current.Status == domainsession.StatusReuseDetected {
		status = domainsession.StatusReuseDetected
	}
	value := sessionStatusValue{
		UserID: current.UserID.String(), SessionID: current.ID.String(), Status: status,
		ExpiresAt: current.ExpiresAt.UTC(), Version: current.Version,
	}
	result, err := s.applyValue(ctx, value, s.terminalTTL(current.ExpiresAt))
	if err != nil {
		return oops.In("auth_session_status").Code("session_status.tombstone_failed").Wrap(err)
	}
	if result == cacheResultCurrentActive {
		return nil
	}
	if err := s.untrack(ctx, current.UserID, current.ID); err != nil {
		return oops.In("auth_session_status").Code("session_status.tombstone_failed").Wrap(err)
	}
	return nil
}

func (s *SessionProjection) applyValue(ctx context.Context, value sessionStatusValue, ttl time.Duration) (int64, error) {
	if value.SessionID == "" || value.Status == "" || value.Version < 0 || ttl <= 0 {
		return 0, errors.New("invalid session status cache write")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	ttlMillis := max(ttl.Milliseconds(), int64(1))
	result, err := applyStatusScript.Run(
		ctx,
		s.redis,
		[]string{sessionStatusKeyPrefix + value.SessionID},
		encoded,
		value.Version,
		value.Status,
		ttlMillis,
	).Int64()
	if err != nil {
		return 0, err
	}
	if result != cacheResultTerminal && result != cacheResultApplied && result != cacheResultCurrentActive {
		return 0, errors.New("invalid session status cache result")
	}
	return result, nil
}

func (s *SessionProjection) trackActive(ctx context.Context, userID, sessionID uuid.UUID) error {
	pipe := s.redis.Pipeline()
	key := userSessionsKeyPrefix + userID.String()
	pipe.SAdd(ctx, key, sessionID.String())
	pipe.Expire(ctx, key, s.activeTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *SessionProjection) untrack(ctx context.Context, userID, sessionID uuid.UUID) error {
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return nil
	}
	return s.redis.SRem(ctx, userSessionsKeyPrefix+userID.String(), sessionID.String()).Err()
}

func (s *SessionProjection) terminalTTL(validUntil time.Time) time.Duration {
	ttl := s.tombstoneTTL
	remaining := validUntil.Sub(s.now().UTC())
	if remaining > 0 && remaining < ttl {
		ttl = remaining
	}
	return ttl
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

var _ applicationsession.StatusProjectionWriter = (*SessionProjection)(nil)
var _ applicationsession.StatusReader = (*SessionProjection)(nil)
var _ applicationsessionprojection.Sink = (*SessionProjection)(nil)
