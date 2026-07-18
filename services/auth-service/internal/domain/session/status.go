package session

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
)

type StatusState string

const (
	StatusActive      StatusState = "active"
	StatusExpired     StatusState = "expired"
	StatusRevoked     StatusState = "revoked"
	StatusRevoking    StatusState = "revoking"
	StatusUnavailable StatusState = "unavailable"
)

var (
	ErrStatusCacheMiss = oops.In("session_status").Code("cache.miss").New("session status cache miss")
	ErrStatusNotFound  = oops.In("session_status").Code("source.not_found").New("session status not found")
)

type StatusRecord struct {
	UserID            uuid.UUID
	SessionID         uuid.UUID
	State             StatusState
	IdleExpiresAt     *time.Time
	AbsoluteExpiresAt time.Time
	Version           int64
	RevokedUntil      *time.Time
}

func (r StatusRecord) RedisFields() map[string]string {
	idleExpiry, revokedUntil := "", ""
	if r.IdleExpiresAt != nil {
		idleExpiry = strconv.FormatInt(r.IdleExpiresAt.Unix(), 10)
	}
	if r.RevokedUntil != nil {
		revokedUntil = strconv.FormatInt(r.RevokedUntil.Unix(), 10)
	}
	return map[string]string{
		"user_id": r.UserID.String(), "session_id": r.SessionID.String(), "status": string(r.State),
		"idle_expires_at": idleExpiry, "absolute_expires_at": strconv.FormatInt(r.AbsoluteExpiresAt.Unix(), 10),
		"status_version": strconv.FormatInt(r.Version, 10), "revoked_until": revokedUntil,
	}
}

type StatusCheck struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	TokenID   uuid.UUID
}

type StatusCache interface {
	Get(context.Context, uuid.UUID) (StatusRecord, error)
	Put(context.Context, StatusRecord, time.Duration) error
	PutActiveIfWritable(context.Context, StatusRecord, time.Duration) (bool, error)
	RestoreActive(context.Context, StatusRecord, int64, time.Duration) (bool, error)
}

type StatusSource interface {
	FindStatus(context.Context, uuid.UUID) (StatusRecord, error)
}

type StatusServiceConfig struct {
	ActiveTTL       time.Duration
	AccessTTL       time.Duration
	FallbackTimeout time.Duration
	MaxFallbacks    int
}

type StatusServiceOptions struct {
	Cache  StatusCache
	Source StatusSource
	Now    func() time.Time
	Config StatusServiceConfig
}

type StatusService struct {
	cache     StatusCache
	source    StatusSource
	now       func() time.Time
	config    StatusServiceConfig
	fallbacks chan struct{}
}

func NewStatusService(options StatusServiceOptions) *StatusService {
	config := options.Config
	if config.ActiveTTL <= 0 || config.ActiveTTL > 5*time.Minute {
		config.ActiveTTL = 5 * time.Minute
	}
	if config.AccessTTL <= 0 {
		config.AccessTTL = 15 * time.Minute
	}
	if config.FallbackTimeout <= 0 || config.FallbackTimeout > 100*time.Millisecond {
		config.FallbackTimeout = 100 * time.Millisecond
	}
	if config.MaxFallbacks < 1 || config.MaxFallbacks > 32 {
		config.MaxFallbacks = 32
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &StatusService{
		cache: options.Cache, source: options.Source, now: now, config: config,
		fallbacks: make(chan struct{}, config.MaxFallbacks),
	}
}

func (s *StatusService) Check(ctx context.Context, check StatusCheck) StatusState {
	if check.UserID == uuid.Nil || check.SessionID == uuid.Nil || check.TokenID == uuid.Nil || s.cache == nil || s.source == nil {
		return StatusUnavailable
	}
	record, err := s.cache.Get(ctx, check.SessionID)
	fenced := err == nil && record.State == StatusRevoking
	if err == nil && !fenced {
		return s.evaluate(record, check)
	}
	if err != nil && !errors.Is(err, ErrStatusCacheMiss) {
		return StatusUnavailable
	}
	select {
	case s.fallbacks <- struct{}{}:
		defer func() { <-s.fallbacks }()
	default:
		return StatusUnavailable
	}
	fallbackCtx, cancel := context.WithTimeout(ctx, s.config.FallbackTimeout)
	defer cancel()
	record, err = s.source.FindStatus(fallbackCtx, check.SessionID)
	if errors.Is(err, ErrStatusNotFound) {
		return StatusRevoked
	}
	if err != nil {
		return StatusUnavailable
	}
	state := s.evaluate(record, check)
	if fenced && state == StatusActive {
		return StatusUnavailable
	}
	if state == StatusUnavailable {
		return state
	}
	ttl := s.cacheTTL(record, state)
	if state == StatusActive {
		written, putErr := s.cache.PutActiveIfWritable(fallbackCtx, record, ttl)
		if putErr != nil || !written {
			return StatusUnavailable
		}
		return state
	}
	if err := s.cache.Put(fallbackCtx, record, ttl); err != nil {
		return StatusUnavailable
	}
	return state
}

func (s *StatusService) Project(ctx context.Context, sessionID uuid.UUID) error {
	if sessionID == uuid.Nil || s.cache == nil || s.source == nil {
		return oops.In("session_status").Code("projection.invalid").New("session status projection is not configured")
	}
	projectionCtx, cancel := context.WithTimeout(ctx, s.config.FallbackTimeout)
	defer cancel()
	record, err := s.source.FindStatus(projectionCtx, sessionID)
	if errors.Is(err, ErrStatusNotFound) {
		return nil
	}
	if err != nil {
		return oops.In("session_status").Code("projection.source_failed").Wrap(err)
	}
	state := s.evaluate(record, StatusCheck{UserID: record.UserID, SessionID: sessionID})
	if state == StatusUnavailable {
		return oops.In("session_status").Code("projection.malformed").New("session status source returned an invalid record")
	}
	if state == StatusActive {
		_, err = s.cache.PutActiveIfWritable(projectionCtx, record, s.cacheTTL(record, state))
	} else {
		err = s.cache.Put(projectionCtx, record, s.cacheTTL(record, state))
	}
	if err != nil {
		return oops.In("session_status").Code("projection.cache_failed").Wrap(err)
	}
	return nil
}

func (s *StatusService) evaluate(record StatusRecord, check StatusCheck) StatusState {
	if record.SessionID != check.SessionID || record.UserID == uuid.Nil || record.AbsoluteExpiresAt.IsZero() || record.Version < 0 {
		return StatusUnavailable
	}
	if record.UserID != check.UserID {
		return StatusRevoked
	}
	now := s.now().UTC()
	if !record.AbsoluteExpiresAt.After(now) || record.IdleExpiresAt != nil && !record.IdleExpiresAt.After(now) {
		return StatusExpired
	}
	switch record.State {
	case StatusActive:
		return StatusActive
	case StatusExpired:
		return StatusExpired
	case StatusRevoked:
		return StatusRevoked
	case StatusRevoking:
		return StatusUnavailable
	default:
		return StatusUnavailable
	}
}

func (s *StatusService) cacheTTL(record StatusRecord, state StatusState) time.Duration {
	now := s.now().UTC()
	switch state {
	case StatusActive:
		ttl := minDuration(s.config.ActiveTTL, record.AbsoluteExpiresAt.Sub(now))
		if record.IdleExpiresAt != nil {
			ttl = minDuration(ttl, record.IdleExpiresAt.Sub(now))
		}
		return positiveTTL(ttl)
	case StatusRevoked:
		if record.RevokedUntil != nil {
			return positiveTTL(record.RevokedUntil.Sub(now))
		}
		return s.config.AccessTTL
	case StatusExpired:
		return s.config.ActiveTTL
	default:
		return time.Second
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func positiveTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Second
	}
	return ttl
}
