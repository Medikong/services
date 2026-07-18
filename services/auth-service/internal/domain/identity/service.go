package identity

import (
	"context"
	"crypto/hmac"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LinkService struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	reauth      ReauthenticationProofConsumer
	identities  Repository
	challenges  challenge.Repository
	sessions    session.Repository
	issue       *session.Service
	idempotency idempotency.Repository
	outbox      outbox.Repository
	virtual     bool
	ttl         time.Duration
	recoveryTTL time.Duration
}

type ReauthenticationProofConsumer interface {
	ConsumeProofID(context.Context, pgx.Tx, string, session.Principal, string) (uuid.UUID, error)
}

func NewLinkService(pool *pgxpool.Pool, keys security.Keys, reauth ReauthenticationProofConsumer, identities Repository, challenges challenge.Repository, sessions session.Repository, issue *session.Service, idempotency idempotency.Repository, outbox outbox.Repository, virtual bool, ttl, recoveryTTL time.Duration) *LinkService {
	return &LinkService{pool: pool, keys: keys, reauth: reauth, identities: identities, challenges: challenges, sessions: sessions, issue: issue, idempotency: idempotency, outbox: outbox, virtual: virtual, ttl: ttl, recoveryTTL: recoveryTTL}
}

type StartLinkInput struct {
	Principal                    session.Principal
	Phone, Proof, IdempotencyKey string
}
type StartLinkOutput struct {
	LinkID    string
	Status    string
	ExpiresAt time.Time
	Existing  bool
}

func (s *LinkService) StartLink(ctx context.Context, input StartLinkInput) (StartLinkOutput, error) {
	phone, err := normalizePhone(input.Phone)
	if err != nil {
		return StartLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	if !validIdempotencyKey(input.IdempotencyKey) {
		return StartLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "멱등성 키가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if output, replayed, err := s.claimOrReplayLinkStart(ctx, tx, "start_method_link", input.Principal, phone, input.Proof, input.IdempotencyKey); err != nil {
		return StartLinkOutput{}, err
	} else if replayed {
		if err := tx.Commit(ctx); err != nil {
			return StartLinkOutput{}, domain.Unavailable()
		}
		return output, nil
	}
	if _, err := s.reauth.ConsumeProofID(ctx, tx, input.Proof, input.Principal, "link_identity"); err != nil {
		return StartLinkOutput{}, err
	}
	existing, findErr := s.identities.FindByValueForUpdate(ctx, tx, TypePhone, phone)
	if findErr == nil {
		link, linkErr := s.identities.FindActiveLinkForIdentity(ctx, tx, existing.ID)
		if linkErr == nil && link.UserID == input.Principal.UserID {
			output := StartLinkOutput{LinkID: link.ID.String(), Status: "active", Existing: true}
			if err := s.storeLinkStartReplay(ctx, tx, "start_method_link", input.Principal, phone, input.Proof, input.IdempotencyKey, output); err != nil {
				return StartLinkOutput{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return StartLinkOutput{}, domain.Unavailable()
			}
			return output, nil
		}
		return StartLinkOutput{}, domain.Problem(409, "AUTH_IDENTITY_LINK_CONFLICT", "이미 사용할 수 없는 휴대폰 인증 수단입니다.")
	} else if !errors.Is(findErr, ErrNotFound) {
		return StartLinkOutput{}, domain.Unavailable()
	}
	identityID, linkID := uuid.New(), uuid.New()
	if err := s.identities.Reserve(ctx, tx, Identity{ID: identityID, Type: TypePhone, NormalizedValue: phone, MaskedValue: maskPhone(phone)}); err != nil {
		return StartLinkOutput{}, mapIdentityError(err)
	}
	expires := time.Now().UTC().Add(s.linkTTL())
	if err := s.identities.CreateRequestedLink(ctx, tx, Link{ID: linkID, Identity: identityID, UserID: input.Principal.UserID, Type: TypePhone, ExpiresAt: &expires}); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	output := StartLinkOutput{LinkID: linkID.String(), Status: "requested", ExpiresAt: expires}
	if err := s.storeLinkStartReplay(ctx, tx, "start_method_link", input.Principal, phone, input.Proof, input.IdempotencyKey, output); err != nil {
		return StartLinkOutput{}, err
	}
	if err := domain.AppendAudit(ctx, tx, "auth.identity_link.requested", "user", input.Principal.UserID, linkID, map[string]string{"method": "phone"}, input.IdempotencyKey); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	return output, nil
}

type IssueLinkInput struct {
	Principal              session.Principal
	LinkID, IdempotencyKey string
}
type IssueLinkOutput struct {
	ChallengeID, Masked string
	ExpiresAt           time.Time
}

func (s *LinkService) IssueIdentityLink(ctx context.Context, input IssueLinkInput) (IssueLinkOutput, error) {
	return s.issueLink(ctx, input, challenge.PurposeIdentityLink, challenge.SubjectIdentityLink)
}

func (s *LinkService) IssuePhoneReplacement(ctx context.Context, input IssueLinkInput) (IssueLinkOutput, error) {
	return s.issueLink(ctx, input, challenge.PurposePhoneChange, challenge.SubjectPhoneChange)
}

func (s *LinkService) issueLink(ctx context.Context, input IssueLinkInput, purpose challenge.Purpose, subjectType challenge.SubjectType) (IssueLinkOutput, error) {
	linkID, err := uuid.Parse(input.LinkID)
	if err != nil {
		return IssueLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "인증 수단 연동 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	link, target, err := s.identities.RequestedLinkForUpdate(ctx, tx, linkID)
	if errors.Is(err, ErrNotFound) || link.UserID != input.Principal.UserID {
		return IssueLinkOutput{}, domain.Problem(404, "AUTH_IDENTITY_LINK_NOT_FOUND", "인증 수단 연동 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	if link.ExpiresAt == nil || !link.ExpiresAt.After(time.Now()) {
		return IssueLinkOutput{}, domain.Problem(410, "AUTH_IDENTITY_LINK_INTENT_EXPIRED", "인증 수단 연동 요청 시간이 만료되었습니다.")
	}
	challengeID := uuid.New()
	code, err := s.keys.VerificationCode()
	if err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	now := time.Now().UTC()
	expires := minTime(now.Add(s.challengeTTL()), *link.ExpiresAt)
	verification, err := challenge.New(challenge.NewInput{ID: challengeID, SubjectType: subjectType, SubjectID: linkID, Purpose: purpose, Method: challenge.MethodPhone, Channel: challenge.ChannelSMSCode, Destination: target.NormalizedValue, DestinationLookupHash: s.keys.Hash("destination", target.NormalizedValue), IdentityID: &target.ID, CodeHash: s.keys.Hash("challenge", challengeID.String(), code), VerifierKeyVersion: 1, MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute), ExpiresAt: expires, CreatedAt: now})
	if err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	if err := s.challenges.Issue(ctx, tx, verification); err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	if err := s.identities.AttachProofChallenge(ctx, tx, linkID, challengeID); err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	ciphertext, err := s.keys.Seal(map[string]string{"code": code, "destination": target.NormalizedValue})
	if err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	deliveryID := uuid.New()
	if err := s.challenges.StoreDeliveryPayload(ctx, tx, challenge.DeliveryPayload{ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: ciphertext, KeyID: "auth-replay-v1", AADHash: s.keys.Hash("delivery", challengeID.String()), ExpiresAt: expires}); err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	if err := s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.IdentityLinkVerificationRequested", AggregateType: "IdentityLink", AggregateID: linkID, Version: 0, Payload: payload(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String()}), CorrelationID: input.Principal.SessionID}); err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	if s.virtual {
		encrypted, err := s.keys.SealVirtual(map[string]string{"code": code})
		if err != nil {
			return IssueLinkOutput{}, domain.Unavailable()
		}
		if err := s.challenges.StoreVirtualProjection(ctx, tx, challenge.VirtualProjection{ChallengeID: challengeID, Channel: challenge.ChannelSMSCode, ChallengeVersion: verification.Version, CodeCiphertext: encrypted, CodeKeyID: "auth-virtual-v1", MaskedDestination: target.MaskedValue, Status: challenge.VirtualReady, ExpiresAt: expires, CreatedAt: now}); err != nil {
			return IssueLinkOutput{}, domain.Unavailable()
		}
	}
	if err := domain.AppendAudit(ctx, tx, "auth.identity_link.challenge_issued", "user", input.Principal.UserID, linkID, map[string]string{"purpose": string(purpose)}, input.IdempotencyKey); err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return IssueLinkOutput{}, domain.Unavailable()
	}
	return IssueLinkOutput{ChallengeID: challengeID.String(), Masked: target.MaskedValue, ExpiresAt: expires}, nil
}

type CompleteLinkInput struct {
	Principal                                 session.Principal
	LinkID, ChallengeID, Code, IdempotencyKey string
	PreviousWebCookie                         string
}
type CompleteLinkOutput struct {
	LinkID string
	Issued session.Issued
}

func (s *LinkService) CompleteIdentityLink(ctx context.Context, input CompleteLinkInput) (CompleteLinkOutput, error) {
	return s.completeLink(ctx, input, challenge.PurposeIdentityLink, challenge.SubjectIdentityLink, false)
}

func (s *LinkService) CompletePhoneReplacement(ctx context.Context, input CompleteLinkInput) (CompleteLinkOutput, error) {
	return s.completeLink(ctx, input, challenge.PurposePhoneChange, challenge.SubjectPhoneChange, true)
}

func (s *LinkService) completeLink(ctx context.Context, input CompleteLinkInput, purpose challenge.Purpose, subjectType challenge.SubjectType, replace bool) (CompleteLinkOutput, error) {
	linkID, err := uuid.Parse(input.LinkID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return CompleteLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "인증 수단 연동 요청이 올바르지 않습니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return CompleteLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if replace {
		if output, replayed, err := s.claimOrReplayPhoneReplacement(ctx, tx, input, linkID, challengeID); err != nil {
			return CompleteLinkOutput{}, err
		} else if replayed {
			if err := tx.Commit(ctx); err != nil {
				return CompleteLinkOutput{}, domain.Unavailable()
			}
			return output, nil
		}
	}
	link, target, err := s.identities.RequestedLinkForUpdate(ctx, tx, linkID)
	if errors.Is(err, ErrNotFound) || link.UserID != input.Principal.UserID {
		return CompleteLinkOutput{}, domain.Problem(404, "AUTH_IDENTITY_LINK_NOT_FOUND", "인증 수단 연동 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	verification, result, err := s.challenges.Consume(ctx, tx, challengeID, time.Now().UTC(), func(current challenge.Challenge) bool {
		return current.SubjectType == subjectType && current.SubjectID == linkID && current.Purpose == purpose && s.keys.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
	})
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if verification.SubjectID != linkID || !result.Verified {
		if err := tx.Commit(ctx); err != nil {
			return CompleteLinkOutput{}, domain.Unavailable()
		}
		if result.Failure == challenge.ConsumeFailureExpired {
			return CompleteLinkOutput{}, domain.Problem(410, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
		}
		return CompleteLinkOutput{}, domain.Problem(400, "AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
	}
	if err := s.identities.MarkVerified(ctx, tx, target.ID); err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if replace {
		if link.PreviousID == nil {
			return CompleteLinkOutput{}, domain.Unavailable()
		}
		if err := s.identities.ReplacePhoneLink(ctx, tx, *link.PreviousID, link.ID); err != nil {
			return CompleteLinkOutput{}, domain.Unavailable()
		}
	} else if err := s.identities.ActivateLink(ctx, tx, link.ID); err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	output := CompleteLinkOutput{LinkID: link.ID.String()}
	if replace {
		issued, err := s.issue.RotateForDeliveryTx(ctx, tx, session.RotationInput{Principal: input.Principal, PreviousWebCookie: input.PreviousWebCookie})
		if err != nil {
			return CompleteLinkOutput{}, err
		}
		output.Issued = issued
		if err := s.storePhoneReplacementReplay(ctx, tx, input, linkID, challengeID, output); err != nil {
			return CompleteLinkOutput{}, err
		}
	}
	if err := domain.AppendAudit(ctx, tx, "auth.identity_link.completed", "user", input.Principal.UserID, linkID, map[string]string{"purpose": string(purpose)}, stableKey(input.IdempotencyKey, "identity-link", challengeID)); err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	var fences domain.RevocationFences
	if replace {
		fences, err = s.sessions.FenceRevocationsForIdentityLinkExcept(ctx, tx, session.IdentityLinkRevocationScope{
			IdentityLinkID: *link.PreviousID,
			KeepSessionID:  input.Principal.SessionID,
		})
		if err != nil {
			domain.ResolveRevocationRollback(ctx, tx, fences)
			return CompleteLinkOutput{}, domain.Unavailable()
		}
		if err := s.sessions.RevokeForIdentityLinkExcept(ctx, tx, *link.PreviousID, input.Principal.SessionID, "phone_replaced"); err != nil {
			domain.ResolveRevocationRollback(ctx, tx, fences)
			return CompleteLinkOutput{}, domain.Unavailable()
		}
	}
	if err := tx.Commit(ctx); err != nil {
		domain.ResolveRevocationRollback(ctx, tx, fences)
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if fences != nil {
		if err := fences.Resolve(ctx); err != nil {
			return CompleteLinkOutput{}, domain.Unavailable()
		}
	}
	return output, nil
}

// RecoverPhoneReplacementWebDelivery accepts a web credential in
// rotated_pending_delivery only for the exact already-completed replacement.
func (s *LinkService) RecoverPhoneReplacementWebDelivery(ctx context.Context, webCookie, csrfToken, linkIDValue, challengeIDValue, code, idempotencyKey string) (CompleteLinkOutput, error) {
	linkID, err := uuid.Parse(linkIDValue)
	if err != nil || strings.TrimSpace(webCookie) == "" || strings.TrimSpace(csrfToken) == "" || len(strings.TrimSpace(code)) != 6 || !validIdempotencyKey(idempotencyKey) {
		return CompleteLinkOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	challengeID, err := uuid.Parse(challengeIDValue)
	if err != nil {
		return CompleteLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, credential, err := s.sessions.FindRecoveryWebSecretForUpdate(ctx, tx, s.keys.Hash(webCookie))
	if errors.Is(err, session.ErrNotFound) {
		return CompleteLinkOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if subtle.ConstantTimeCompare(credential.CSRFHash, s.keys.Hash("csrf", csrfToken)) != 1 {
		return CompleteLinkOutput{}, domain.Problem(403, "AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	if credential.DeliveryRecoveryExpiresAt == nil || !credential.DeliveryRecoveryExpiresAt.After(time.Now().UTC()) {
		return CompleteLinkOutput{}, deliveryExpired()
	}
	output, err := s.replayPhoneReplacement(ctx, tx, current.ID, linkID, challengeID, code, idempotencyKey)
	if err != nil {
		return CompleteLinkOutput{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	return output, nil
}

type ReplacementInput struct {
	Principal                    session.Principal
	Phone, Proof, IdempotencyKey string
}

func (s *LinkService) StartReplacement(ctx context.Context, input ReplacementInput) (StartLinkOutput, error) {
	phone, err := normalizePhone(input.Phone)
	if err != nil {
		return StartLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	if !validIdempotencyKey(input.IdempotencyKey) {
		return StartLinkOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "멱등성 키가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if output, replayed, err := s.claimOrReplayLinkStart(ctx, tx, "start_phone_replacement", input.Principal, phone, input.Proof, input.IdempotencyKey); err != nil {
		return StartLinkOutput{}, err
	} else if replayed {
		if err := tx.Commit(ctx); err != nil {
			return StartLinkOutput{}, domain.Unavailable()
		}
		return output, nil
	}
	proofID, err := s.reauth.ConsumeProofID(ctx, tx, input.Proof, input.Principal, "replace_phone")
	if err != nil {
		return StartLinkOutput{}, err
	}
	previous, _, err := s.identities.FindActiveLinkForUserType(ctx, tx, input.Principal.UserID, TypePhone)
	if err != nil {
		return StartLinkOutput{}, domain.Problem(409, "AUTH_IDENTITY_LINK_CONFLICT", "교체할 휴대폰 인증 수단이 없습니다.")
	}
	if _, err := s.identities.FindByValueForUpdate(ctx, tx, TypePhone, phone); err == nil {
		return StartLinkOutput{}, domain.Problem(409, "AUTH_IDENTITY_LINK_CONFLICT", "이미 사용할 수 없는 휴대폰 인증 수단입니다.")
	} else if !errors.Is(err, ErrNotFound) {
		return StartLinkOutput{}, domain.Unavailable()
	}
	identityID, linkID := uuid.New(), uuid.New()
	if err := s.identities.Reserve(ctx, tx, Identity{ID: identityID, Type: TypePhone, NormalizedValue: phone, MaskedValue: maskPhone(phone)}); err != nil {
		return StartLinkOutput{}, mapIdentityError(err)
	}
	expires := time.Now().UTC().Add(s.linkTTL())
	if err := s.identities.CreatePhoneReplacementRequested(ctx, tx, Link{ID: linkID, Identity: identityID, UserID: input.Principal.UserID, Type: TypePhone, ExpiresAt: &expires}, previous.ID, proofID); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	output := StartLinkOutput{LinkID: linkID.String(), Status: "requested", ExpiresAt: expires}
	if err := s.storeLinkStartReplay(ctx, tx, "start_phone_replacement", input.Principal, phone, input.Proof, input.IdempotencyKey, output); err != nil {
		return StartLinkOutput{}, err
	}
	if err := domain.AppendAudit(ctx, tx, "auth.phone_replacement.requested", "user", input.Principal.UserID, linkID, map[string]string{"method": "phone"}, input.IdempotencyKey); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	return output, nil
}

// Link-start commands consume a one-time reauthentication proof.  Persisting
// their result before commit makes an uncertain client retry safe: it replays
// the original Link instead of attempting to consume the proof again.
func (s *LinkService) claimOrReplayLinkStart(ctx context.Context, tx pgx.Tx, operation string, principal session.Principal, phone, proof, key string) (StartLinkOutput, bool, error) {
	scopeHash := s.keys.Hash(operation, principal.SessionID.String())
	requestHash := s.keys.Hash(operation, phone, proof)
	candidate := idempotency.NewRecord(operation, scopeHash, s.keys.Hash(key), requestHash, nil, nil, time.Now().UTC().Add(s.linkTTL()))
	record, claimed, err := s.idempotency.ClaimProcessing(ctx, tx, candidate, "IdentityLink")
	if err != nil {
		return StartLinkOutput{}, false, domain.Unavailable()
	}
	if claimed {
		return StartLinkOutput{}, false, nil
	}
	if !hmac.Equal(record.RequestHash, requestHash) {
		return StartLinkOutput{}, false, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return StartLinkOutput{}, false, domain.Unavailable()
	}
	output, err := s.openLinkStartReplay(ctx, tx, *record.ReplayID, operation, principal.SessionID, key)
	return output, true, err
}

func (s *LinkService) storeLinkStartReplay(ctx context.Context, tx pgx.Tx, operation string, principal session.Principal, phone, proof, key string, output StartLinkOutput) error {
	scopeHash := s.keys.Hash(operation, principal.SessionID.String())
	record, err := s.idempotency.FindForUpdate(ctx, tx, operation, scopeHash, s.keys.Hash(key))
	if err != nil || !hmac.Equal(record.RequestHash, s.keys.Hash(operation, phone, proof)) {
		return domain.Unavailable()
	}
	ciphertext, err := s.keys.Seal(output)
	if err != nil {
		return domain.Unavailable()
	}
	replayID := uuid.New()
	if err := s.idempotency.CreateReplayPayload(ctx, tx, idempotency.ReplayPayload{ID: replayID, Kind: "identity_link_start_result", Ciphertext: ciphertext, BindingHash: s.keys.Hash(operation, principal.SessionID.String(), key), ExpiresAt: record.ExpiresAt}); err != nil {
		return domain.Unavailable()
	}
	if err := s.idempotency.AttachReplayPayload(ctx, tx, record.ID, replayID); err != nil {
		return domain.Unavailable()
	}
	if err := s.idempotency.Complete(ctx, tx, record.ID, "completed"); err != nil {
		return domain.Unavailable()
	}
	return nil
}

func (s *LinkService) openLinkStartReplay(ctx context.Context, tx pgx.Tx, replayID uuid.UUID, operation string, sessionID uuid.UUID, key string) (StartLinkOutput, error) {
	payload, err := s.idempotency.FindReplayPayloadForUpdate(ctx, tx, replayID)
	if errors.Is(err, idempotency.ErrNotFound) {
		return StartLinkOutput{}, domain.Problem(410, "AUTH_REAUTHENTICATION_PROOF_INVALID", "재인증 권한이 만료되었거나 이미 사용되었습니다.")
	}
	if err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	if payload.Kind != "identity_link_start_result" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(time.Now().UTC()) || !hmac.Equal(payload.BindingHash, s.keys.Hash(operation, sessionID.String(), key)) {
		return StartLinkOutput{}, domain.Problem(410, "AUTH_REAUTHENTICATION_PROOF_INVALID", "재인증 권한이 만료되었거나 이미 사용되었습니다.")
	}
	var output StartLinkOutput
	if err := s.keys.Open(payload.Ciphertext, &output); err != nil || output.LinkID == "" {
		return StartLinkOutput{}, domain.Unavailable()
	}
	if err := s.idempotency.RecordReplay(ctx, tx, replayID); err != nil {
		return StartLinkOutput{}, domain.Unavailable()
	}
	return output, nil
}

func (s *LinkService) claimOrReplayPhoneReplacement(ctx context.Context, tx pgx.Tx, input CompleteLinkInput, linkID, challengeID uuid.UUID) (CompleteLinkOutput, bool, error) {
	scopeHash := s.keys.Hash("complete_phone_replacement", input.Principal.SessionID.String(), linkID.String())
	keyHash := s.keys.Hash(input.IdempotencyKey)
	requestHash := s.phoneReplacementRequestHash(challengeID, input.Code)
	candidate := idempotency.NewRecord("complete_phone_replacement", scopeHash, keyHash, requestHash, &linkID, nil, time.Now().UTC().Add(s.recoveryTTLValue()))
	record, claimed, err := s.idempotency.ClaimProcessing(ctx, tx, candidate, "IdentityLink")
	if err != nil {
		return CompleteLinkOutput{}, false, domain.Unavailable()
	}
	if claimed {
		return CompleteLinkOutput{}, false, nil
	}
	if !hmac.Equal(record.RequestHash, requestHash) {
		return CompleteLinkOutput{}, false, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return CompleteLinkOutput{}, false, domain.Unavailable()
	}
	output, err := s.openPhoneReplacementReplay(ctx, tx, *record.ReplayID, input.Principal.SessionID, linkID, input.IdempotencyKey)
	return output, true, err
}

func (s *LinkService) replayPhoneReplacement(ctx context.Context, tx pgx.Tx, sessionID, linkID, challengeID uuid.UUID, code, key string) (CompleteLinkOutput, error) {
	scopeHash := s.keys.Hash("complete_phone_replacement", sessionID.String(), linkID.String())
	record, err := s.idempotency.FindForUpdate(ctx, tx, "complete_phone_replacement", scopeHash, s.keys.Hash(key))
	if errors.Is(err, idempotency.ErrNotFound) {
		return CompleteLinkOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if !hmac.Equal(record.RequestHash, s.phoneReplacementRequestHash(challengeID, code)) {
		return CompleteLinkOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	return s.openPhoneReplacementReplay(ctx, tx, *record.ReplayID, sessionID, linkID, key)
}

func (s *LinkService) storePhoneReplacementReplay(ctx context.Context, tx pgx.Tx, input CompleteLinkInput, linkID, challengeID uuid.UUID, output CompleteLinkOutput) error {
	scopeHash := s.keys.Hash("complete_phone_replacement", input.Principal.SessionID.String(), linkID.String())
	record, err := s.idempotency.FindForUpdate(ctx, tx, "complete_phone_replacement", scopeHash, s.keys.Hash(input.IdempotencyKey))
	if err != nil {
		return domain.Unavailable()
	}
	ciphertext, err := s.keys.Seal(output)
	if err != nil {
		return domain.Unavailable()
	}
	replayID := uuid.New()
	if err := s.idempotency.CreateReplayPayload(ctx, tx, idempotency.ReplayPayload{ID: replayID, Kind: "phone_replacement_credential_delivery", Ciphertext: ciphertext, BindingHash: s.phoneReplacementReplayBinding(input.Principal.SessionID, linkID, input.IdempotencyKey), ExpiresAt: record.ExpiresAt}); err != nil {
		return domain.Unavailable()
	}
	if err := s.idempotency.AttachReplayPayload(ctx, tx, record.ID, replayID); err != nil {
		return domain.Unavailable()
	}
	if err := s.idempotency.Complete(ctx, tx, record.ID, "completed"); err != nil {
		return domain.Unavailable()
	}
	return nil
}

func (s *LinkService) openPhoneReplacementReplay(ctx context.Context, tx pgx.Tx, replayID, sessionID, linkID uuid.UUID, key string) (CompleteLinkOutput, error) {
	payload, err := s.idempotency.FindReplayPayloadForUpdate(ctx, tx, replayID)
	if errors.Is(err, idempotency.ErrNotFound) {
		return CompleteLinkOutput{}, deliveryExpired()
	}
	if err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if payload.Kind != "phone_replacement_credential_delivery" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(time.Now().UTC()) || !hmac.Equal(payload.BindingHash, s.phoneReplacementReplayBinding(sessionID, linkID, key)) {
		return CompleteLinkOutput{}, deliveryExpired()
	}
	var output CompleteLinkOutput
	if err := s.keys.Open(payload.Ciphertext, &output); err != nil || output.LinkID != linkID.String() || output.Issued.SessionID != sessionID.String() {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	if err := s.idempotency.RecordReplay(ctx, tx, replayID); err != nil {
		return CompleteLinkOutput{}, domain.Unavailable()
	}
	return output, nil
}

func (s *LinkService) phoneReplacementRequestHash(challengeID uuid.UUID, code string) []byte {
	return s.keys.Hash("complete_phone_replacement", challengeID.String(), code)
}

func (s *LinkService) phoneReplacementReplayBinding(sessionID, linkID uuid.UUID, key string) []byte {
	return s.keys.Hash("complete_phone_replacement", sessionID.String(), linkID.String(), key)
}

func (s *LinkService) recoveryTTLValue() time.Duration {
	if s.recoveryTTL > 0 {
		return s.recoveryTTL
	}
	return 5 * time.Minute
}

func (s *LinkService) linkTTL() time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	return 10 * time.Minute
}
func (s *LinkService) challengeTTL() time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	return 10 * time.Minute
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func payload(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func stableKey(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}
func normalizePhone(value string) (string, error) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "-", "")
	if !strings.HasPrefix(value, "+") || len(value) < 8 {
		return "", errors.New("phone")
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return "", errors.New("phone")
		}
	}
	return value, nil
}
func maskPhone(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:3] + strings.Repeat("*", len(value)-5) + value[len(value)-2:]
}
func mapIdentityError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.Problem(409, "AUTH_IDENTITY_LINK_CONFLICT", "이미 사용할 수 없는 휴대폰 인증 수단입니다.")
	}
	return domain.Unavailable()
}

func validIdempotencyKey(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func deliveryExpired() error {
	return domain.Problem(410, "AUTH_SESSION_DELIVERY_EXPIRED", "Session credential 전달 복구 시간이 만료되었습니다.")
}
