package passwordreset

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	sessiondomain "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	ResetTTL              time.Duration
	ChallengeTTL          time.Duration
	VirtualAdapterEnabled bool
}

type Service struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	config      Config
	bootstrap   *intent.BootstrapService
	resets      Repository
	identities  identity.Repository
	challenges  challenge.Repository
	idempotency idempotency.Repository
	sessions    sessiondomain.Repository
	outbox      outbox.Repository
}

func NewService(pool *pgxpool.Pool, keys security.Keys, config Config, bootstrap *intent.BootstrapService, resets Repository, identities identity.Repository, challenges challenge.Repository, idempotency idempotency.Repository, sessions sessiondomain.Repository, outbox outbox.Repository) *Service {
	return &Service{pool: pool, keys: keys, config: config, bootstrap: bootstrap, resets: resets, identities: identities, challenges: challenges, idempotency: idempotency, sessions: sessions, outbox: outbox}
}

type StartInput struct{ IntentID, OwnerProof, CSRFToken, IdentifierType, Email, Phone, IdempotencyKey string }
type StartOutput struct {
	ResetID   string
	ExpiresAt time.Time
}

func (s *Service) Start(ctx context.Context, input StartInput) (StartOutput, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "Idempotency-Key 헤더가 필요합니다.")
	}
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil {
		return StartOutput{}, domain.Problem(404, "AUTH_INTENT_NOT_FOUND", "인증 요청을 찾을 수 없습니다.")
	}
	identifierType := identity.Type(strings.TrimSpace(input.IdentifierType))
	value, err := normalizeIdentifier(identifierType, input.Email, input.Phone)
	if err != nil {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "재설정 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	currentIntent, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, intentID, input.OwnerProof, input.CSRFToken, true)
	if err != nil {
		return StartOutput{}, err
	}
	scope, keyHash, requestHash := s.keys.Hash("start_password_reset", intentID.String()), s.keys.Hash(input.IdempotencyKey), s.keys.Hash(string(identifierType), value)
	record, err := s.idempotency.FindForUpdate(ctx, tx, "start_password_reset", scope, keyHash)
	if err == nil {
		if !hmac.Equal(record.RequestHash, requestHash) || record.ResourceID == nil {
			return StartOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
		reset, findErr := s.resets.FindForUpdate(ctx, tx, *record.ResourceID)
		if errors.Is(findErr, ErrNotFound) {
			return StartOutput{}, domain.Problem(410, "AUTH_INTENT_EXPIRED", "비밀번호 재설정 요청 시간이 만료되었습니다.")
		}
		if findErr != nil {
			return StartOutput{}, domain.Unavailable()
		}
		if err := tx.Commit(ctx); err != nil {
			return StartOutput{}, domain.Unavailable()
		}
		return StartOutput{ResetID: reset.ID.String(), ExpiresAt: reset.ExpiresAt}, nil
	}
	if !errors.Is(err, idempotency.ErrNotFound) {
		return StartOutput{}, domain.Unavailable()
	}
	var identityID *uuid.UUID
	actual, findErr := s.identities.FindByValueForUpdate(ctx, tx, identifierType, value)
	if findErr == nil {
		identityID = &actual.ID
	} else if !errors.Is(findErr, identity.ErrNotFound) {
		return StartOutput{}, domain.Unavailable()
	}
	now := time.Now().UTC()
	expires := minTime(now.Add(s.resetTTL()), currentIntent.ExpiresAt)
	resetID := uuid.New()
	reset, err := New(resetID, &currentIntent.ID, identityID, expires, now)
	if err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := s.resets.Create(ctx, tx, reset); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := s.idempotency.CreateCompleted(ctx, tx, idempotency.NewRecord("start_password_reset", scope, keyHash, requestHash, &resetID, nil, expires), "PasswordReset", "accepted"); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.PasswordResetRequested", AggregateType: "PasswordReset", AggregateID: resetID, Version: 0, Payload: eventPayload(map[string]string{"passwordResetId": resetID.String()}), CorrelationID: currentIntent.ID}); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := domain.AppendAudit(ctx, tx, "auth.password_reset.requested", "authentication_intent", currentIntent.ID, resetID, map[string]string{"status": "accepted"}, input.IdempotencyKey); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	return StartOutput{ResetID: resetID.String(), ExpiresAt: expires}, nil
}

type IssueInput struct{ ResetID, OwnerProof, CSRFToken, Method, IdempotencyKey string }
type IssueOutput struct {
	ChallengeID string
	ExpiresAt   time.Time
}

func (s *Service) Issue(ctx context.Context, input IssueInput) (IssueOutput, error) {
	resetID, err := uuid.Parse(input.ResetID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return IssueOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "재설정 Challenge 요청이 올바르지 않습니다.")
	}
	method := identity.Type(strings.TrimSpace(input.Method))
	if method != identity.TypeEmail && method != identity.TypePhone {
		return IssueOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "확인 수단이 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	reset, err := s.resets.FindForUpdate(ctx, tx, resetID)
	if errors.Is(err, ErrNotFound) {
		return IssueOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	if reset.IntentID == nil {
		return IssueOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if _, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, *reset.IntentID, input.OwnerProof, input.CSRFToken, true); err != nil {
		return IssueOutput{}, err
	}
	if reset.Status != StatusRequested || !reset.ExpiresAt.After(time.Now()) {
		return IssueOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	challengeID := uuid.New()
	now := time.Now().UTC()
	expires := minTime(now.Add(s.challengeTTL()), reset.ExpiresAt)
	code, err := s.keys.VerificationCode()
	if err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	var destination, masked string
	var identityID *uuid.UUID
	channel := challenge.ChannelEmailCode
	if reset.IdentityID != nil {
		target, findErr := s.identities.FindByIDForUpdate(ctx, tx, *reset.IdentityID)
		if findErr != nil {
			return IssueOutput{}, domain.Unavailable()
		}
		if target.Type != method {
			return IssueOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "선택한 확인 수단을 사용할 수 없습니다.")
		}
		destination, masked = target.NormalizedValue, target.MaskedValue
		identityID = &target.ID
		if method == identity.TypePhone {
			channel = challenge.ChannelSMSCode
		}
	} else {
		destination = "decoy:" + challengeID.String()
		masked = "***"
		if method == identity.TypePhone {
			channel = challenge.ChannelSMSCode
		}
	}
	verification, err := challenge.New(challenge.NewInput{ID: challengeID, SubjectType: challenge.SubjectPasswordReset, SubjectID: resetID, Purpose: challenge.PurposePasswordReset, Method: challenge.Method(method), Channel: channel, Destination: destination, DestinationLookupHash: s.keys.Hash("destination", destination), IdentityID: identityID, CodeHash: s.keys.Hash("challenge", challengeID.String(), code), VerifierKeyVersion: 1, MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute), ExpiresAt: expires, CreatedAt: now})
	if err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	if err := s.challenges.Issue(ctx, tx, verification); err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	if err := reset.AttachChallenge(challengeID); err != nil {
		return IssueOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err := s.resets.Save(ctx, tx, &reset); err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	if reset.IdentityID != nil {
		ciphertext, sealErr := s.keys.Seal(map[string]string{"code": code, "destination": destination})
		if sealErr != nil {
			return IssueOutput{}, domain.Unavailable()
		}
		deliveryID := uuid.New()
		if err := s.challenges.StoreDeliveryPayload(ctx, tx, challenge.DeliveryPayload{ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: ciphertext, KeyID: "auth-replay-v1", AADHash: s.keys.Hash("delivery", challengeID.String()), ExpiresAt: expires}); err != nil {
			return IssueOutput{}, domain.Unavailable()
		}
		if err := s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.PasswordResetVerificationRequested", AggregateType: "PasswordReset", AggregateID: resetID, Version: reset.Version, Payload: eventPayload(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String()}), CorrelationID: *reset.IntentID}); err != nil {
			return IssueOutput{}, domain.Unavailable()
		}
		if s.config.VirtualAdapterEnabled {
			virtual, sealErr := s.keys.SealVirtual(map[string]string{"code": code})
			if sealErr != nil {
				return IssueOutput{}, domain.Unavailable()
			}
			if err := s.challenges.StoreVirtualProjection(ctx, tx, challenge.VirtualProjection{ChallengeID: challengeID, Channel: channel, ChallengeVersion: verification.Version, CodeCiphertext: virtual, CodeKeyID: "auth-virtual-v1", MaskedDestination: masked, Status: challenge.VirtualReady, ExpiresAt: expires, CreatedAt: now}); err != nil {
				return IssueOutput{}, domain.Unavailable()
			}
		}
	}
	if err := domain.AppendAudit(ctx, tx, "auth.password_reset.challenge_issued", "authentication_intent", *reset.IntentID, resetID, map[string]string{"method": string(method)}, input.IdempotencyKey); err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return IssueOutput{}, domain.Unavailable()
	}
	return IssueOutput{ChallengeID: challengeID.String(), ExpiresAt: expires}, nil
}

type VerifyInput struct{ ResetID, ChallengeID, OwnerProof, CSRFToken, Code, Channel, IdempotencyKey string }
type VerifyOutput struct {
	ResetID    string
	ExpiresAt  time.Time
	ResetGrant string
}

func (s *Service) Verify(ctx context.Context, input VerifyInput) (VerifyOutput, error) {
	resetID, err := uuid.Parse(input.ResetID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return VerifyOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "확인 코드가 올바르지 않습니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return VerifyOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	reset, err := s.resets.FindForUpdate(ctx, tx, resetID)
	if errors.Is(err, ErrNotFound) || reset.IntentID == nil {
		return VerifyOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	if _, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, *reset.IntentID, input.OwnerProof, input.CSRFToken, true); err != nil {
		return VerifyOutput{}, err
	}
	verification, result, err := s.challenges.Consume(ctx, tx, challengeID, time.Now().UTC(), func(current challenge.Challenge) bool {
		return current.SubjectType == challenge.SubjectPasswordReset && current.SubjectID == resetID && s.keys.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
	})
	if err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	if verification.SubjectType != challenge.SubjectPasswordReset || verification.SubjectID != resetID {
		// The deferred rollback preserves the unrelated Challenge attempt count.
		return VerifyOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if reset.ChallengeID == nil || *reset.ChallengeID != challengeID {
		return VerifyOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if !result.Verified {
		if err := tx.Commit(ctx); err != nil {
			return VerifyOutput{}, domain.Unavailable()
		}
		if result.Failure == challenge.ConsumeFailureExpired {
			return VerifyOutput{}, domain.Problem(410, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
		}
		return VerifyOutput{}, domain.Problem(400, "AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
	}
	if reset.IdentityID == nil {
		if err := tx.Commit(ctx); err != nil {
			return VerifyOutput{}, domain.Unavailable()
		}
		return VerifyOutput{ResetID: resetID.String(), ExpiresAt: reset.ExpiresAt}, nil
	}
	grant, err := s.keys.Opaque("rgr_")
	if err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	if reset.Status == StatusRequested {
		if err := reset.Verify(s.keys.Hash(resetID.String(), grant), time.Now().UTC()); err != nil {
			return VerifyOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
		}
	} else if reset.Status == StatusChallengeVerified {
		reset.ResetGrantHash = s.keys.Hash(resetID.String(), grant)
	} else {
		return VerifyOutput{}, domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err := s.resets.Save(ctx, tx, &reset); err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	if err := domain.AppendAudit(ctx, tx, "auth.password_reset.verified", "authentication_intent", *reset.IntentID, resetID, map[string]string{"status": "verified"}, stableKey(input.IdempotencyKey, "verify-reset", challengeID)); err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return VerifyOutput{}, domain.Unavailable()
	}
	if input.Channel != "web" {
		return VerifyOutput{ResetID: resetID.String(), ExpiresAt: reset.ExpiresAt, ResetGrant: grant}, nil
	}
	return VerifyOutput{ResetID: resetID.String(), ExpiresAt: reset.ExpiresAt}, nil
}

type CompleteInput struct{ ResetID, OwnerProof, CSRFToken, Channel, ResetGrant, NewPassword, ConfirmPassword, IdempotencyKey string }

func (s *Service) Complete(ctx context.Context, input CompleteInput) error {
	resetID, err := uuid.Parse(input.ResetID)
	if err != nil || input.NewPassword != input.ConfirmPassword || strings.TrimSpace(input.IdempotencyKey) == "" {
		return domain.Problem(400, "AUTH_INPUT_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err := (security.PasswordPolicy{}).Validate(input.NewPassword); err != nil {
		return domain.Problem(422, "AUTH_PASSWORD_POLICY_NOT_MET", "비밀번호 정책을 만족하지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	reset, err := s.resets.FindForUpdate(ctx, tx, resetID)
	if errors.Is(err, ErrNotFound) || reset.IntentID == nil || reset.IdentityID == nil {
		return domain.Problem(400, "AUTH_PASSWORD_RESET_INVALID", "비밀번호 재설정 요청이 올바르지 않습니다.")
	}
	if err != nil {
		return domain.Unavailable()
	}
	if _, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, *reset.IntentID, input.OwnerProof, input.CSRFToken, true); err != nil {
		return err
	}
	if reset.Status != StatusChallengeVerified || !reset.ExpiresAt.After(time.Now()) {
		return domain.Problem(410, "AUTH_PASSWORD_RESET_GRANT_EXPIRED", "비밀번호 재설정 권한이 만료되었습니다.")
	}
	if input.Channel != "web" && !s.keys.Equal(reset.ResetGrantHash, resetID.String(), input.ResetGrant) {
		return domain.Problem(410, "AUTH_PASSWORD_RESET_GRANT_EXPIRED", "비밀번호 재설정 권한이 만료되었습니다.")
	}
	hash, err := security.HashPassword(input.NewPassword)
	if err != nil {
		return domain.Unavailable()
	}
	if err := s.identities.ReplacePasswordCredential(ctx, tx, *reset.IdentityID, hash); err != nil {
		return domain.Unavailable()
	}
	link, err := s.identities.FindActiveLinkForIdentity(ctx, tx, *reset.IdentityID)
	if err != nil {
		return domain.Unavailable()
	}
	if err := s.sessions.RevokeForUser(ctx, tx, link.UserID, "password_reset"); err != nil {
		return domain.Unavailable()
	}
	if err := reset.Complete(time.Now().UTC()); err != nil {
		return domain.Problem(410, "AUTH_PASSWORD_RESET_GRANT_EXPIRED", "비밀번호 재설정 권한이 만료되었습니다.")
	}
	if err := s.resets.Save(ctx, tx, &reset); err != nil {
		return domain.Unavailable()
	}
	if err := s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.PasswordResetCompleted", AggregateType: "PasswordReset", AggregateID: resetID, Version: reset.Version, Payload: eventPayload(map[string]string{"passwordResetId": resetID.String()}), CorrelationID: *reset.IntentID}); err != nil {
		return domain.Unavailable()
	}
	if err := domain.AppendAudit(ctx, tx, "auth.password_reset.completed", "authentication_intent", *reset.IntentID, resetID, map[string]string{"status": "completed"}, input.IdempotencyKey); err != nil {
		return domain.Unavailable()
	}
	return tx.Commit(ctx)
}

func (s *Service) resetTTL() time.Duration {
	if s.config.ResetTTL > 0 {
		return s.config.ResetTTL
	}
	return 15 * time.Minute
}
func (s *Service) challengeTTL() time.Duration {
	if s.config.ChallengeTTL > 0 {
		return s.config.ChallengeTTL
	}
	return 10 * time.Minute
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func eventPayload(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func stableKey(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}
func normalizeIdentifier(kind identity.Type, email, phone string) (string, error) {
	if kind == identity.TypeEmail {
		value := strings.ToLower(strings.TrimSpace(email))
		if strings.Count(value, "@") != 1 || len(value) < 3 {
			return "", errors.New("email")
		}
		return value, nil
	}
	if kind == identity.TypePhone {
		value := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(phone), " ", ""), "-", "")
		if !strings.HasPrefix(value, "+") || len(value) < 8 {
			return "", errors.New("phone")
		}
		return value, nil
	}
	return "", errors.New("type")
}
