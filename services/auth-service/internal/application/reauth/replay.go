package reauth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/google/uuid"
)

func (s *Service) claimOrReplay(ctx context.Context, repository IdempotencyRepository, input Input) (Output, bool, error) {
	scopeHash := s.cryptography.Hash("reauthenticate_email", input.Principal.SessionID.String(), input.Purpose)
	requestHash := s.reauthRequestHash(input.Purpose, input.Password)
	sessionID := input.Principal.SessionID
	candidate := domainidempotency.Record{
		ID: uuid.New(), Operation: "reauthenticate_email", ScopeHash: scopeHash,
		KeyHash: s.cryptography.Hash(input.IdempotencyKey), RequestHash: requestHash,
		ResourceID: &sessionID, ExpiresAt: s.clock.Now().UTC().Add(s.recoveryTTL()),
	}
	record, claimed, err := repository.ClaimProcessing(ctx, candidate, "Session")
	if err != nil {
		return Output{}, false, unavailable(err)
	}
	if claimed {
		return Output{}, false, nil
	}
	if !s.cryptography.Equal(record.RequestHash, "reauthenticate_email", input.Purpose, input.Password) {
		return Output{}, false, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return Output{}, false, unavailable(nil)
	}
	output, err := s.openReplay(ctx, repository, *record.ReplayID, input.Principal.SessionID, input.Purpose, input.IdempotencyKey)
	return output, true, err
}

func (s *Service) replay(ctx context.Context, repository IdempotencyRepository, sessionID uuid.UUID, purpose, password, key string) (Output, error) {
	scopeHash := s.cryptography.Hash("reauthenticate_email", sessionID.String(), purpose)
	record, err := repository.FindForUpdate(ctx, "reauthenticate_email", scopeHash, s.cryptography.Hash(key))
	if errors.Is(err, domainidempotency.ErrNotFound) {
		return Output{}, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return Output{}, unavailable(err)
	}
	if !s.cryptography.Equal(record.RequestHash, "reauthenticate_email", purpose, password) {
		return Output{}, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return Output{}, unavailable(nil)
	}
	return s.openReplay(ctx, repository, *record.ReplayID, sessionID, purpose, key)
}

func (s *Service) storeReplay(ctx context.Context, repository IdempotencyRepository, input Input, output Output) error {
	scopeHash := s.cryptography.Hash("reauthenticate_email", input.Principal.SessionID.String(), input.Purpose)
	record, err := repository.FindForUpdate(ctx, "reauthenticate_email", scopeHash, s.cryptography.Hash(input.IdempotencyKey))
	if err != nil {
		return unavailable(err)
	}
	ciphertext, err := s.cryptography.SealOutput(output)
	if err != nil {
		return unavailable(err)
	}
	replayID := uuid.New()
	if err := repository.CreateReplayPayload(ctx, domainidempotency.ReplayPayload{
		ID: replayID, Kind: "reauthentication_credential_delivery", Ciphertext: ciphertext,
		BindingHash: s.replayBinding(input.Principal.SessionID, input.Purpose, input.IdempotencyKey), ExpiresAt: record.ExpiresAt,
	}); err != nil {
		return unavailable(err)
	}
	if err := repository.AttachReplayPayload(ctx, record.ID, replayID); err != nil {
		return unavailable(err)
	}
	if err := repository.Complete(ctx, record.ID, "completed"); err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Service) openReplay(ctx context.Context, repository IdempotencyRepository, replayID, sessionID uuid.UUID, purpose, key string) (Output, error) {
	payload, err := repository.FindReplayPayloadForUpdate(ctx, replayID)
	if errors.Is(err, domainidempotency.ErrNotFound) {
		return Output{}, deliveryExpired()
	}
	if err != nil {
		return Output{}, unavailable(err)
	}
	if payload.Kind != "reauthentication_credential_delivery" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(s.clock.Now().UTC()) || !s.cryptography.Equal(payload.BindingHash, "reauthenticate_email", sessionID.String(), purpose, key) {
		return Output{}, deliveryExpired()
	}
	output, err := s.cryptography.OpenOutput(payload.Ciphertext)
	if err != nil || output.Issued.SessionID != sessionID.String() || output.Purpose != purpose {
		return Output{}, unavailable(err)
	}
	if err := repository.RecordReplay(ctx, replayID); err != nil {
		return Output{}, unavailable(err)
	}
	return output, nil
}

func (s *Service) reauthRequestHash(purpose, password string) []byte {
	return s.cryptography.Hash("reauthenticate_email", purpose, password)
}

func (s *Service) replayBinding(sessionID uuid.UUID, purpose, key string) []byte {
	return s.cryptography.Hash("reauthenticate_email", sessionID.String(), purpose, key)
}

func (s *Service) proofTTL() time.Duration {
	if s.config.ProofTTL > 0 {
		return s.config.ProofTTL
	}
	return 5 * time.Minute
}

func (s *Service) recoveryTTL() time.Duration {
	if s.config.RecoveryTTL > 0 {
		return s.config.RecoveryTTL
	}
	return 5 * time.Minute
}

func validIdempotencyKey(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func stableKey(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}

func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

func validPurpose(value string) bool {
	return value == "link_identity" || value == "replace_phone"
}
