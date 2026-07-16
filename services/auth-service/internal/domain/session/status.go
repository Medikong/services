package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

const (
	sessionStatusKeyPrefix = "auth:session-status:"
	userSessionsKeyPrefix  = "auth:user-sessions:"
)

type StatusProjectionWriter interface {
	RevokeSession(context.Context, uuid.UUID) error
	RevokeUser(context.Context, uuid.UUID) error
}

type StatusProjection struct {
	repository        *PostgresRepository
	redis             *redis.Client
	timeout           time.Duration
	dbFallbackTimeout time.Duration
}

type statusValue struct {
	UserID    string    `json:"userId"`
	SessionID string    `json:"sessionId"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func NewStatusProjection(repository *PostgresRepository, client *redis.Client, timeout, dbFallbackTimeout time.Duration) (*StatusProjection, error) {
	if repository == nil || client == nil || timeout <= 0 || dbFallbackTimeout <= 0 || dbFallbackTimeout > timeout {
		return nil, oops.In("auth_session_status").Code("session_status.invalid_config").New("invalid Session status projection configuration")
	}
	return &StatusProjection{repository: repository, redis: client, timeout: timeout, dbFallbackTimeout: dbFallbackTimeout}, nil
}

// Check returns false without an error for a rejected credential. Storage or
// cache failures return an error so the HTTP adapter can fail closed with 503.
func (s *StatusProjection) Check(ctx context.Context, claims security.Claims) (bool, error) {
	userID, userErr := uuid.Parse(claims.Subject)
	sessionID, sessionErr := uuid.Parse(claims.SessionID)
	if userErr != nil || sessionErr != nil {
		return false, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	key := sessionStatusKeyPrefix + sessionID.String()
	encoded, err := s.redis.Get(checkCtx, key).Bytes()
	if err == nil {
		var cached statusValue
		if json.Unmarshal(encoded, &cached) != nil {
			return false, oops.In("auth_session_status").Code("session_status.cache_invalid").New("Session status cache value is invalid")
		}
		return cached.Status == "active" && cached.UserID == userID.String() && cached.SessionID == sessionID.String() && time.Now().UTC().Before(cached.ExpiresAt), nil
	}
	if !errors.Is(err, redis.Nil) {
		return false, oops.In("auth_session_status").Code("session_status.cache_unavailable").Wrap(err)
	}

	dbCtx, cancelDB := context.WithTimeout(checkCtx, s.dbFallbackTimeout)
	current, err := s.repository.FindStatus(dbCtx, sessionID)
	cancelDB()
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, oops.In("auth_session_status").Code("session_status.database_unavailable").Wrap(err)
	}
	if current.Status != "active" || current.UserID != userID || !time.Now().UTC().Before(current.ExpiresAt) {
		return false, nil
	}
	if err := s.writeActive(checkCtx, current); err != nil {
		return false, err
	}
	return true, nil
}

func (s *StatusProjection) writeActive(ctx context.Context, current Session) error {
	ttl := time.Until(current.ExpiresAt)
	if ttl <= 0 {
		return nil
	}
	encoded, err := json.Marshal(statusValue{
		UserID: current.UserID.String(), SessionID: current.ID.String(), Status: current.Status, ExpiresAt: current.ExpiresAt.UTC(),
	})
	if err != nil {
		return oops.In("auth_session_status").Code("session_status.encode_failed").Wrap(err)
	}
	userKey := userSessionsKeyPrefix + current.UserID.String()
	pipe := s.redis.TxPipeline()
	pipe.Set(ctx, sessionStatusKeyPrefix+current.ID.String(), encoded, ttl)
	pipe.SAdd(ctx, userKey, current.ID.String())
	// Keep the reverse index for at least as long as its longest-lived Session.
	// ExpireNX handles a new set; ExpireGT prevents a shorter Session from
	// reducing the lifetime needed to invalidate an older one.
	pipe.ExpireNX(ctx, userKey, ttl)
	pipe.ExpireGT(ctx, userKey, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return oops.In("auth_session_status").Code("session_status.write_through_failed").Wrap(err)
	}
	return nil
}

func (s *StatusProjection) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	if sessionID == uuid.Nil {
		return oops.In("auth_session_status").Code("session_status.session_required").New("Session ID is required")
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.redis.Del(writeCtx, sessionStatusKeyPrefix+sessionID.String()).Err(); err != nil {
		return oops.In("auth_session_status").Code("session_status.revoke_failed").Wrap(err)
	}
	return nil
}

func (s *StatusProjection) RevokeUser(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return oops.In("auth_session_status").Code("session_status.user_required").New("user ID is required")
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	userKey := userSessionsKeyPrefix + userID.String()
	sessionIDs, err := s.redis.SMembers(writeCtx, userKey).Result()
	if err != nil {
		return oops.In("auth_session_status").Code("session_status.user_lookup_failed").Wrap(err)
	}
	keys := make([]string, 0, len(sessionIDs)+1)
	keys = append(keys, userKey)
	for _, sessionID := range sessionIDs {
		if parsed, parseErr := uuid.Parse(sessionID); parseErr == nil {
			keys = append(keys, sessionStatusKeyPrefix+parsed.String())
		}
	}
	if err := s.redis.Del(writeCtx, keys...).Err(); err != nil {
		return oops.In("auth_session_status").Code("session_status.user_revoke_failed").Wrap(err)
	}
	return nil
}
