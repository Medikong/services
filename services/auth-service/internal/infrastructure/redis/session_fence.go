package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

var fenceStatusScript = goredis.NewScript(`
local incoming = ARGV[1]
local incoming_version = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
local current_raw = redis.call("GET", KEYS[1])

if current_raw then
  local decoded_ok, current = pcall(cjson.decode, current_raw)
  if decoded_ok and type(current) == "table" then
    local current_status = tostring(current.status or "")
    local current_version = tonumber(current.version)
    if current_status ~= "active" then
      return 0
    end
    if current_version and current_version > incoming_version then
      return 2
    end
  end
end

redis.call("PSETEX", KEYS[1], ttl_ms, incoming)
return 1
`)

var resolveStatusFenceScript = goredis.NewScript(`
local current_raw = redis.call("GET", KEYS[1])
if not current_raw then
  return 0
end

local decoded_ok, current = pcall(cjson.decode, current_raw)
if not decoded_ok or type(current) ~= "table" then
  return 0
end
if tostring(current.status or "") ~= "revoking" or tostring(current.fenceId or "") ~= ARGV[1] then
  return 0
end

redis.call("PSETEX", KEYS[1], tonumber(ARGV[3]), ARGV[2])
return 1
`)

type sessionFenceEntry struct {
	sessionID uuid.UUID
	fenceID   string
}

type sessionRevocationFence struct {
	projection *SessionProjection
	entries    []sessionFenceEntry
}

// Fence places fail-closed markers before the owning database transaction
// changes the sessions. A partial write still returns a resolver for markers
// that were already installed.
func (s *SessionProjection) Fence(ctx context.Context, sessions []domainsession.Session) (domainsession.RevocationFence, error) {
	fence := &sessionRevocationFence{projection: s}
	if s == nil || s.redis == nil || s.repository == nil {
		return fence, oops.In("auth_session_status_fence").Code("fence.not_configured").
			New("Session status fence is not configured")
	}
	for _, current := range sessions {
		if current.ID == uuid.Nil || current.UserID == uuid.Nil || current.Status != "active" ||
			current.Version < 0 || current.ExpiresAt.IsZero() {
			return fence, oops.In("auth_session_status_fence").Code("fence.invalid_session").
				New("cannot fence invalid Session status")
		}
	}

	writeCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	for _, current := range sessions {
		entry := sessionFenceEntry{sessionID: current.ID, fenceID: uuid.NewString()}
		value := sessionStatusValue{
			UserID: current.UserID.String(), SessionID: current.ID.String(), Status: "revoking",
			ExpiresAt: current.ExpiresAt.UTC(), Version: current.Version, FenceID: entry.fenceID,
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fence, oops.In("auth_session_status_fence").Code("fence.encode_failed").Wrap(err)
		}
		// Keep the entry before I/O because a network error can arrive after
		// Redis applied the script. Resolve is safe when the marker was not set.
		fence.entries = append(fence.entries, entry)
		result, err := fenceStatusScript.Run(
			writeCtx,
			s.redis,
			[]string{sessionStatusKeyPrefix + current.ID.String()},
			encoded,
			current.Version,
			max(s.terminalTTL(current.ExpiresAt).Milliseconds(), int64(1)),
		).Int64()
		if err != nil {
			return fence, oops.In("auth_session_status_fence").Code("fence.write_failed").Wrap(err)
		}
		switch result {
		case cacheResultTerminal:
			fence.entries = fence.entries[:len(fence.entries)-1]
			continue
		case cacheResultApplied:
		case cacheResultCurrentActive:
			fence.entries = fence.entries[:len(fence.entries)-1]
			return fence, oops.In("auth_session_status_fence").Code("fence.newer_active").
				New("cannot replace a newer active Session status")
		default:
			return fence, oops.In("auth_session_status_fence").Code("fence.invalid_result").
				New("invalid Session status fence result")
		}
	}
	return fence, nil
}

func (f *sessionRevocationFence) Resolve(ctx context.Context) error {
	if f == nil || f.projection == nil {
		return nil
	}
	var resolutionErrors []error
	for _, entry := range f.entries {
		if err := f.projection.resolveFence(ctx, entry); err != nil {
			resolutionErrors = append(resolutionErrors, err)
		}
	}
	return errors.Join(resolutionErrors...)
}

func (s *SessionProjection) resolveFence(ctx context.Context, entry sessionFenceEntry) error {
	resolveCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	dbCtx, cancelDB := context.WithTimeout(resolveCtx, s.dbFallbackTimeout)
	current, err := s.repository.FindStatus(dbCtx, entry.sessionID)
	if err == nil {
		err = dbCtx.Err()
	}
	cancelDB()
	if errors.Is(err, domainsession.ErrNotFound) {
		return nil
	}
	if err != nil {
		return oops.In("auth_session_status_fence").Code("fence.source_failed").Wrap(err)
	}
	_, err = s.resolveFenceFromStatus(resolveCtx, entry, current)
	return err
}

func (s *SessionProjection) resolveFenceFromStatus(
	ctx context.Context,
	entry sessionFenceEntry,
	current domainsession.Session,
) (bool, error) {
	value, ttl, active, err := s.resolvedFenceValue(current)
	if err != nil {
		return false, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, oops.In("auth_session_status_fence").Code("fence.encode_failed").Wrap(err)
	}
	result, err := resolveStatusFenceScript.Run(
		ctx,
		s.redis,
		[]string{sessionStatusKeyPrefix + entry.sessionID.String()},
		entry.fenceID,
		encoded,
		max(ttl.Milliseconds(), int64(1)),
	).Int64()
	if err != nil {
		return false, oops.In("auth_session_status_fence").Code("fence.resolve_failed").Wrap(err)
	}
	if result == 0 {
		return false, nil
	}
	if result != 1 {
		return false, oops.In("auth_session_status_fence").Code("fence.invalid_result").
			New("invalid Session status fence resolution result")
	}
	if active {
		if err := s.trackActive(ctx, current.UserID, current.ID); err != nil {
			return false, oops.In("auth_session_status_fence").Code("fence.index_failed").Wrap(err)
		}
		return true, nil
	}
	if err := s.untrack(ctx, current.UserID, current.ID); err != nil {
		return false, oops.In("auth_session_status_fence").Code("fence.index_failed").Wrap(err)
	}
	return false, nil
}

func (s *SessionProjection) resolvedFenceValue(current domainsession.Session) (sessionStatusValue, time.Duration, bool, error) {
	now := s.now().UTC()
	if current.ID == uuid.Nil || current.UserID == uuid.Nil || current.Version < 0 || current.ExpiresAt.IsZero() {
		return sessionStatusValue{}, 0, false, oops.In("auth_session_status_fence").Code("fence.source_invalid").
			New("cannot resolve fence from invalid Session status")
	}
	if current.Status == "active" && now.Before(current.ExpiresAt) {
		return sessionStatusValue{
			UserID: current.UserID.String(), SessionID: current.ID.String(), Status: "active",
			ExpiresAt: current.ExpiresAt.UTC(), Version: current.Version,
		}, minDuration(current.ExpiresAt.Sub(now), s.activeTTL), true, nil
	}
	status := domainsession.StatusRevoked
	if current.Status == domainsession.StatusReuseDetected {
		status = domainsession.StatusReuseDetected
	} else if current.Status != "active" && current.Status != domainsession.StatusRevoked {
		return sessionStatusValue{}, 0, false, oops.In("auth_session_status_fence").Code("fence.source_invalid").
			New("cannot resolve fence from invalid Session status")
	}
	return sessionStatusValue{
		UserID: current.UserID.String(), SessionID: current.ID.String(), Status: status,
		ExpiresAt: current.ExpiresAt.UTC(), Version: current.Version,
	}, s.terminalTTL(current.ExpiresAt), false, nil
}
