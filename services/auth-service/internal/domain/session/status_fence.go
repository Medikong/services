package session

import (
	"context"
	"encoding/binary"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/samber/oops"
)

type StatusFence struct {
	SessionID uuid.UUID
	Version   int64
}

type StatusFenceSet struct {
	status *StatusService
	fences []StatusFence
}

func (s StatusFenceSet) Resolve(ctx context.Context) error {
	var resolutionErrors []error
	for _, fence := range s.fences {
		if err := s.status.resolveFence(ctx, fence); err != nil {
			resolutionErrors = append(resolutionErrors, err)
		}
	}
	return errors.Join(resolutionErrors...)
}

func (s *StatusService) fence(ctx context.Context, record StatusRecord) (StatusFence, error) {
	if record.SessionID == uuid.Nil || record.UserID == uuid.Nil || record.State != StatusActive || record.AbsoluteExpiresAt.IsZero() {
		return StatusFence{}, oops.In("session_status_fence").Code("record.invalid").New("cannot fence invalid session status")
	}
	id := uuid.New()
	version := int64(binary.BigEndian.Uint64(id[:8]) & uint64(math.MaxInt64))
	if version == 0 {
		version = 1
	}
	record.State = StatusRevoking
	record.Version = version
	now := s.now().UTC()
	revokedUntil := now.Add(s.config.AccessTTL)
	record.RevokedUntil = &revokedUntil
	fenceCtx, cancel := context.WithTimeout(ctx, s.config.FallbackTimeout)
	defer cancel()
	if err := s.cache.Put(fenceCtx, record, revokedUntil.Sub(now)); err != nil {
		return StatusFence{}, oops.In("session_status_fence").Code("cache.write_failed").Wrap(err)
	}
	return StatusFence{SessionID: record.SessionID, Version: version}, nil
}

func (s *StatusService) resolveFence(ctx context.Context, fence StatusFence) error {
	projectionCtx, cancel := context.WithTimeout(ctx, s.config.FallbackTimeout)
	defer cancel()
	record, err := s.source.FindStatus(projectionCtx, fence.SessionID)
	if errors.Is(err, ErrStatusNotFound) {
		return nil
	}
	if err != nil {
		return oops.In("session_status_fence").Code("source.read_failed").Wrap(err)
	}
	state := s.evaluate(record, StatusCheck{UserID: record.UserID, SessionID: record.SessionID})
	if state == StatusUnavailable {
		return oops.In("session_status_fence").Code("source.malformed").New("cannot resolve fence from invalid source status")
	}
	if state == StatusActive {
		_, err = s.cache.RestoreActive(projectionCtx, record, fence.Version, s.cacheTTL(record, state))
	} else {
		err = s.cache.Put(projectionCtx, record, s.cacheTTL(record, state))
	}
	if err != nil {
		return oops.In("session_status_fence").Code("cache.write_failed").Wrap(err)
	}
	return nil
}
