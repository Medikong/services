package identity

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func (s *Service) claimOrReplayLinkStart(ctx context.Context, repository IdempotencyRepository, operation string, principal domainsession.Principal, phone, proof, key string) (StartLinkOutput, bool, error) {
	scopeHash := s.cryptography.Hash(operation, principal.SessionID.String())
	requestHash := s.cryptography.Hash(operation, phone, proof)
	candidate := domainidempotency.NewRecord(operation, scopeHash, s.cryptography.Hash(key), requestHash, nil, nil, s.clock.Now().UTC().Add(s.linkTTL()))
	record, claimed, err := repository.ClaimProcessing(ctx, candidate, "IdentityLink")
	if err != nil {
		return StartLinkOutput{}, false, unavailable(err)
	}
	if claimed {
		return StartLinkOutput{}, false, nil
	}
	if !s.cryptography.Equal(record.RequestHash, operation, phone, proof) {
		return StartLinkOutput{}, false, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return StartLinkOutput{}, false, unavailable(nil)
	}
	output, err := s.openLinkStartReplay(ctx, repository, *record.ReplayID, operation, principal.SessionID, key)
	return output, true, err
}

func (s *Service) storeLinkStartReplay(ctx context.Context, repository IdempotencyRepository, operation string, principal domainsession.Principal, phone, proof, key string, output StartLinkOutput) error {
	scopeHash := s.cryptography.Hash(operation, principal.SessionID.String())
	record, err := repository.FindForUpdate(ctx, operation, scopeHash, s.cryptography.Hash(key))
	if err != nil || !s.cryptography.Equal(record.RequestHash, operation, phone, proof) {
		return unavailable(err)
	}
	ciphertext, err := s.cryptography.SealStartOutput(output)
	if err != nil {
		return unavailable(err)
	}
	replayID := uuid.New()
	if err := repository.CreateReplayPayload(ctx, domainidempotency.ReplayPayload{
		ID: replayID, Kind: "identity_link_start_result", Ciphertext: ciphertext,
		BindingHash: s.cryptography.Hash(operation, principal.SessionID.String(), key), ExpiresAt: record.ExpiresAt,
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

func (s *Service) openLinkStartReplay(ctx context.Context, repository IdempotencyRepository, replayID uuid.UUID, operation string, sessionID uuid.UUID, key string) (StartLinkOutput, error) {
	payload, err := repository.FindReplayPayloadForUpdate(ctx, replayID)
	if errors.Is(err, domainidempotency.ErrNotFound) {
		return StartLinkOutput{}, proofInvalid()
	}
	if err != nil {
		return StartLinkOutput{}, unavailable(err)
	}
	if payload.Kind != "identity_link_start_result" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(s.clock.Now().UTC()) || !s.cryptography.Equal(payload.BindingHash, operation, sessionID.String(), key) {
		return StartLinkOutput{}, proofInvalid()
	}
	output, err := s.cryptography.OpenStartOutput(payload.Ciphertext)
	if err != nil || output.LinkID == "" {
		return StartLinkOutput{}, unavailable(err)
	}
	if err := repository.RecordReplay(ctx, replayID); err != nil {
		return StartLinkOutput{}, unavailable(err)
	}
	return output, nil
}

func (s *Service) claimOrReplayPhoneReplacement(ctx context.Context, repository IdempotencyRepository, input CompleteLinkInput, linkID, challengeID uuid.UUID) (CompleteLinkOutput, bool, error) {
	scopeHash := s.cryptography.Hash("complete_phone_replacement", input.Principal.SessionID.String(), linkID.String())
	requestHash := s.phoneReplacementRequestHash(challengeID, input.Code)
	candidate := domainidempotency.NewRecord("complete_phone_replacement", scopeHash, s.cryptography.Hash(input.IdempotencyKey), requestHash, &linkID, nil, s.clock.Now().UTC().Add(s.recoveryTTL()))
	record, claimed, err := repository.ClaimProcessing(ctx, candidate, "IdentityLink")
	if err != nil {
		return CompleteLinkOutput{}, false, unavailable(err)
	}
	if claimed {
		return CompleteLinkOutput{}, false, nil
	}
	if !s.cryptography.Equal(record.RequestHash, "complete_phone_replacement", challengeID.String(), input.Code) {
		return CompleteLinkOutput{}, false, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return CompleteLinkOutput{}, false, unavailable(nil)
	}
	output, err := s.openPhoneReplacementReplay(ctx, repository, *record.ReplayID, input.Principal.SessionID, linkID, input.IdempotencyKey)
	return output, true, err
}

func (s *Service) replayPhoneReplacement(ctx context.Context, repository IdempotencyRepository, sessionID, linkID, challengeID uuid.UUID, code, key string) (CompleteLinkOutput, error) {
	scopeHash := s.cryptography.Hash("complete_phone_replacement", sessionID.String(), linkID.String())
	record, err := repository.FindForUpdate(ctx, "complete_phone_replacement", scopeHash, s.cryptography.Hash(key))
	if errors.Is(err, domainidempotency.ErrNotFound) {
		return CompleteLinkOutput{}, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return CompleteLinkOutput{}, unavailable(err)
	}
	if !s.cryptography.Equal(record.RequestHash, "complete_phone_replacement", challengeID.String(), code) {
		return CompleteLinkOutput{}, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return CompleteLinkOutput{}, unavailable(nil)
	}
	return s.openPhoneReplacementReplay(ctx, repository, *record.ReplayID, sessionID, linkID, key)
}

func (s *Service) storePhoneReplacementReplay(ctx context.Context, repository IdempotencyRepository, input CompleteLinkInput, linkID, challengeID uuid.UUID, output CompleteLinkOutput) error {
	scopeHash := s.cryptography.Hash("complete_phone_replacement", input.Principal.SessionID.String(), linkID.String())
	record, err := repository.FindForUpdate(ctx, "complete_phone_replacement", scopeHash, s.cryptography.Hash(input.IdempotencyKey))
	if err != nil {
		return unavailable(err)
	}
	ciphertext, err := s.cryptography.SealCompleteOutput(output)
	if err != nil {
		return unavailable(err)
	}
	replayID := uuid.New()
	if err := repository.CreateReplayPayload(ctx, domainidempotency.ReplayPayload{
		ID: replayID, Kind: "phone_replacement_credential_delivery", Ciphertext: ciphertext,
		BindingHash: s.phoneReplacementReplayBinding(input.Principal.SessionID, linkID, input.IdempotencyKey), ExpiresAt: record.ExpiresAt,
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

func (s *Service) openPhoneReplacementReplay(ctx context.Context, repository IdempotencyRepository, replayID, sessionID, linkID uuid.UUID, key string) (CompleteLinkOutput, error) {
	payload, err := repository.FindReplayPayloadForUpdate(ctx, replayID)
	if errors.Is(err, domainidempotency.ErrNotFound) {
		return CompleteLinkOutput{}, deliveryExpired()
	}
	if err != nil {
		return CompleteLinkOutput{}, unavailable(err)
	}
	if payload.Kind != "phone_replacement_credential_delivery" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(s.clock.Now().UTC()) || !s.cryptography.Equal(payload.BindingHash, "complete_phone_replacement", sessionID.String(), linkID.String(), key) {
		return CompleteLinkOutput{}, deliveryExpired()
	}
	output, err := s.cryptography.OpenCompleteOutput(payload.Ciphertext)
	if err != nil || output.LinkID != linkID.String() || output.Issued.SessionID != sessionID.String() {
		return CompleteLinkOutput{}, unavailable(err)
	}
	if err := repository.RecordReplay(ctx, replayID); err != nil {
		return CompleteLinkOutput{}, unavailable(err)
	}
	return output, nil
}

func (s *Service) phoneReplacementRequestHash(challengeID uuid.UUID, code string) []byte {
	return s.cryptography.Hash("complete_phone_replacement", challengeID.String(), code)
}

func (s *Service) phoneReplacementReplayBinding(sessionID, linkID uuid.UUID, key string) []byte {
	return s.cryptography.Hash("complete_phone_replacement", sessionID.String(), linkID.String(), key)
}
