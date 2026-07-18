package session

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

type redisStatusClient interface {
	HGetAll(context.Context, string) *redis.MapStringStringCmd
	TxPipelined(context.Context, func(redis.Pipeliner) error) ([]redis.Cmder, error)
	Eval(context.Context, string, []string, ...interface{}) *redis.Cmd
}

const putActiveIfWritableScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  return 0
end
redis.call('DEL', KEYS[1])
redis.call('HSET', KEYS[1], unpack(ARGV, 2))
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return 1`

const restoreActiveScript = `
if redis.call('HGET', KEYS[1], 'status') ~= 'revoking' or redis.call('HGET', KEYS[1], 'status_version') ~= ARGV[1] then
  return 0
end
redis.call('DEL', KEYS[1])
redis.call('HSET', KEYS[1], unpack(ARGV, 3))
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1`

type RedisStatusCache struct {
	client redisStatusClient
}

func NewRedisStatusCache(client redisStatusClient) *RedisStatusCache {
	return &RedisStatusCache{client: client}
}

func RedisStatusKey(sessionID uuid.UUID) string {
	return "auth:session-status:{" + sessionID.String() + "}"
}

func (c *RedisStatusCache) Get(ctx context.Context, sessionID uuid.UUID) (StatusRecord, error) {
	fields, err := c.client.HGetAll(ctx, RedisStatusKey(sessionID)).Result()
	if err != nil {
		return StatusRecord{}, oops.In("session_status_cache").Code("redis.read_failed").Wrap(err)
	}
	if len(fields) == 0 {
		return StatusRecord{}, ErrStatusCacheMiss
	}
	return parseRedisStatusRecord(fields)
}

func (c *RedisStatusCache) Put(ctx context.Context, record StatusRecord, ttl time.Duration) error {
	key := RedisStatusKey(record.SessionID)
	_, err := c.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		pipe.HSet(ctx, key, record.RedisFields())
		pipe.Expire(ctx, key, positiveTTL(ttl))
		return nil
	})
	if err != nil {
		return oops.In("session_status_cache").Code("redis.write_failed").Wrap(err)
	}
	return nil
}

func (c *RedisStatusCache) PutActiveIfWritable(ctx context.Context, record StatusRecord, ttl time.Duration) (bool, error) {
	args := []interface{}{positiveTTL(ttl).Milliseconds()}
	args = append(args, redisStatusArgs(record)...)
	written, err := c.client.Eval(ctx, putActiveIfWritableScript, []string{RedisStatusKey(record.SessionID)}, args...).Int64()
	if err != nil {
		return false, oops.In("session_status_cache").Code("redis.write_failed").Wrap(err)
	}
	return written == 1, nil
}

func (c *RedisStatusCache) RestoreActive(ctx context.Context, record StatusRecord, fenceVersion int64, ttl time.Duration) (bool, error) {
	args := []interface{}{strconv.FormatInt(fenceVersion, 10), positiveTTL(ttl).Milliseconds()}
	args = append(args, redisStatusArgs(record)...)
	written, err := c.client.Eval(ctx, restoreActiveScript, []string{RedisStatusKey(record.SessionID)}, args...).Int64()
	if err != nil {
		return false, oops.In("session_status_cache").Code("redis.write_failed").Wrap(err)
	}
	return written == 1, nil
}

func redisStatusArgs(record StatusRecord) []interface{} {
	fields := record.RedisFields()
	return []interface{}{
		"user_id", fields["user_id"], "session_id", fields["session_id"], "status", fields["status"],
		"idle_expires_at", fields["idle_expires_at"], "absolute_expires_at", fields["absolute_expires_at"],
		"status_version", fields["status_version"], "revoked_until", fields["revoked_until"],
	}
}

func parseRedisStatusRecord(fields map[string]string) (StatusRecord, error) {
	errBuilder := oops.In("session_status_cache").Code("redis.malformed")
	if len(fields) != 7 {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	for _, field := range [...]string{
		"user_id", "session_id", "status", "idle_expires_at", "absolute_expires_at", "status_version", "revoked_until",
	} {
		if _, exists := fields[field]; !exists {
			return StatusRecord{}, errBuilder.New("malformed session status cache entry")
		}
	}
	userID, err := uuid.Parse(fields["user_id"])
	if err != nil {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	sessionID, err := uuid.Parse(fields["session_id"])
	if err != nil {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	state := StatusState(fields["status"])
	if state != StatusActive && state != StatusExpired && state != StatusRevoked && state != StatusRevoking {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	absoluteExpiry, err := parseUnixTime(fields["absolute_expires_at"])
	if err != nil {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	version, err := strconv.ParseInt(fields["status_version"], 10, 64)
	if err != nil || version < 0 {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	idleExpiry, err := parseOptionalUnixTime(fields["idle_expires_at"])
	if err != nil {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	revokedUntil, err := parseOptionalUnixTime(fields["revoked_until"])
	if err != nil {
		return StatusRecord{}, errBuilder.New("malformed session status cache entry")
	}
	return StatusRecord{
		UserID: userID, SessionID: sessionID, State: state, IdleExpiresAt: idleExpiry,
		AbsoluteExpiresAt: absoluteExpiry, Version: version, RevokedUntil: revokedUntil,
	}, nil
}

func parseUnixTime(raw string) (time.Time, error) {
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func parseOptionalUnixTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := parseUnixTime(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
