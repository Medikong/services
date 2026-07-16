package reauth

import (
	"context"
	"crypto/hmac"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReauthService struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	identities  identity.Repository
	proofs      Repository
	sessions    session.Repository
	idempotency idempotency.Repository
	issue       *session.Service
	proofTTL    time.Duration
	recoveryTTL time.Duration
}

func NewReauthService(pool *pgxpool.Pool, keys security.Keys, identities identity.Repository, proofs Repository, sessions session.Repository, idempotency idempotency.Repository, issue *session.Service, proofTTL, recoveryTTL time.Duration) *ReauthService {
	return &ReauthService{pool: pool, keys: keys, identities: identities, proofs: proofs, sessions: sessions, idempotency: idempotency, issue: issue, proofTTL: proofTTL, recoveryTTL: recoveryTTL}
}

func (s *ReauthService) Reauthenticate(ctx context.Context, input identity.ReauthInput) (identity.ReauthOutput, error) {
	if !input.Principal.Authenticated || !validPurpose(input.Purpose) || strings.TrimSpace(input.Password) == "" || !validIdempotencyKey(input.IdempotencyKey) {
		return identity.ReauthOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "재인증 요청이 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if output, replayed, err := s.claimOrReplay(ctx, tx, input.Principal.SessionID, input.Purpose, input.Password, input.IdempotencyKey); err != nil {
		return identity.ReauthOutput{}, err
	} else if replayed {
		if err := tx.Commit(ctx); err != nil {
			return identity.ReauthOutput{}, domain.Unavailable()
		}
		return output, nil
	}
	identityValue, credential, err := s.identities.FindActiveEmailCredentialForUser(ctx, tx, input.Principal.UserID)
	if errors.Is(err, identity.ErrNotFound) || !security.VerifyPassword(credential.Hash, input.Password) {
		return identity.ReauthOutput{}, domain.Problem(401, "AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
	}
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	link, err := s.identities.FindActiveLinkForIdentity(ctx, tx, identityValue.ID)
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	if link.UserID != input.Principal.UserID {
		return identity.ReauthOutput{}, domain.Problem(401, "AUTH_SIGNIN_FAILED", "이메일 또는 비밀번호가 올바르지 않습니다.")
	}
	issued, err := s.issue.RotateForDeliveryTx(ctx, tx, session.RotationInput{
		Principal: input.Principal, PreviousWebCookie: input.PreviousWebCookie,
		Rebind: &session.SessionRebind{IdentityID: identityValue.ID, IdentityLink: link.ID, Method: "email_password"},
	})
	if err != nil {
		return identity.ReauthOutput{}, err
	}
	proof, err := s.keys.Opaque("rap_")
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	expires := minReauthTime(time.Now().UTC().Add(s.proofTTLValue()), issued.ExpiresAt)
	if err := s.proofs.Create(ctx, tx, Proof{ID: uuid.New(), Hash: s.keys.Hash("reauth", proof), UserID: input.Principal.UserID, SessionID: input.Principal.SessionID, IdentityID: &identityValue.ID, Purpose: input.Purpose, ExpiresAt: expires, CreatedAt: time.Now().UTC()}); err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	output := identity.ReauthOutput{Proof: proof, Purpose: input.Purpose, ExpiresAt: expires, Issued: issued}
	if err := s.storeReplay(ctx, tx, input.Principal.SessionID, input.Purpose, input.Password, input.IdempotencyKey, output); err != nil {
		return identity.ReauthOutput{}, err
	}
	if err := domain.AppendAudit(ctx, tx, "auth.reauthentication.completed", "user", input.Principal.UserID, input.Principal.SessionID, map[string]string{"purpose": input.Purpose}, stableKey(input.IdempotencyKey, "reauth", input.Principal.SessionID)); err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	return output, nil
}

// RecoverWebDelivery accepts an old web credential only while it is marked
// rotated_pending_delivery, and only for the exact reauthentication replay
// that created its successor. It is never a general authentication path.
func (s *ReauthService) RecoverWebDelivery(ctx context.Context, webCookie, csrfToken, purpose, password, idempotencyKey string) (identity.ReauthOutput, error) {
	if strings.TrimSpace(webCookie) == "" || strings.TrimSpace(csrfToken) == "" || !validPurpose(purpose) || strings.TrimSpace(password) == "" || !validIdempotencyKey(idempotencyKey) {
		return identity.ReauthOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, credential, err := s.sessions.FindRecoveryWebSecretForUpdate(ctx, tx, s.keys.Hash(webCookie))
	if errors.Is(err, session.ErrNotFound) {
		return identity.ReauthOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	if subtle.ConstantTimeCompare(credential.CSRFHash, s.keys.Hash("csrf", csrfToken)) != 1 {
		return identity.ReauthOutput{}, domain.Problem(403, "AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	if credential.DeliveryRecoveryExpiresAt == nil || !credential.DeliveryRecoveryExpiresAt.After(time.Now().UTC()) {
		return identity.ReauthOutput{}, deliveryExpired()
	}
	output, err := s.replay(ctx, tx, current.ID, purpose, password, idempotencyKey)
	if err != nil {
		return identity.ReauthOutput{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	return output, nil
}
func (s *ReauthService) ConsumeProofID(ctx context.Context, tx pgx.Tx, raw string, principal session.Principal, purpose string) (uuid.UUID, error) {
	proof, err := s.proofs.FindActiveForUpdate(ctx, tx, s.keys.Hash("reauth", raw), principal.UserID, principal.SessionID, purpose)
	if errors.Is(err, ErrNotFound) {
		return uuid.Nil, domain.Problem(410, "AUTH_REAUTHENTICATION_PROOF_INVALID", "재인증 권한이 만료되었거나 이미 사용되었습니다.")
	}
	if err != nil {
		return uuid.Nil, domain.Unavailable()
	}
	if err := s.proofs.Consume(ctx, tx, proof.ID); err != nil {
		return uuid.Nil, domain.Unavailable()
	}
	return proof.ID, nil
}
func (s *ReauthService) proofTTLValue() time.Duration {
	if s.proofTTL > 0 {
		return s.proofTTL
	}
	return 5 * time.Minute
}

func (s *ReauthService) claimOrReplay(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, purpose, password, key string) (identity.ReauthOutput, bool, error) {
	scopeHash := s.keys.Hash("reauthenticate_email", sessionID.String(), purpose)
	keyHash := s.keys.Hash(key)
	requestHash := s.reauthRequestHash(purpose, password)
	candidate := idempotency.NewRecord("reauthenticate_email", scopeHash, keyHash, requestHash, &sessionID, nil, time.Now().UTC().Add(s.issueRecoveryTTL()))
	record, claimed, err := s.idempotency.ClaimProcessing(ctx, tx, candidate, "Session")
	if err != nil {
		return identity.ReauthOutput{}, false, domain.Unavailable()
	}
	if claimed {
		return identity.ReauthOutput{}, false, nil
	}
	if !hmac.Equal(record.RequestHash, requestHash) {
		return identity.ReauthOutput{}, false, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return identity.ReauthOutput{}, false, domain.Unavailable()
	}
	output, err := s.openReplay(ctx, tx, *record.ReplayID, sessionID, purpose, key)
	return output, true, err
}

func (s *ReauthService) replay(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, purpose, password, key string) (identity.ReauthOutput, error) {
	scopeHash := s.keys.Hash("reauthenticate_email", sessionID.String(), purpose)
	record, err := s.idempotency.FindForUpdate(ctx, tx, "reauthenticate_email", scopeHash, s.keys.Hash(key))
	if errors.Is(err, idempotency.ErrNotFound) {
		return identity.ReauthOutput{}, domain.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	if !hmac.Equal(record.RequestHash, s.reauthRequestHash(purpose, password)) {
		return identity.ReauthOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	if record.Status != "completed" || record.ReplayID == nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	return s.openReplay(ctx, tx, *record.ReplayID, sessionID, purpose, key)
}

func (s *ReauthService) storeReplay(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID, purpose, password, key string, output identity.ReauthOutput) error {
	scopeHash := s.keys.Hash("reauthenticate_email", sessionID.String(), purpose)
	record, err := s.idempotency.FindForUpdate(ctx, tx, "reauthenticate_email", scopeHash, s.keys.Hash(key))
	if err != nil {
		return domain.Unavailable()
	}
	ciphertext, err := s.keys.Seal(output)
	if err != nil {
		return domain.Unavailable()
	}
	replayID := uuid.New()
	if err := s.idempotency.CreateReplayPayload(ctx, tx, idempotency.ReplayPayload{ID: replayID, Kind: "reauthentication_credential_delivery", Ciphertext: ciphertext, BindingHash: s.replayBinding(sessionID, purpose, key), ExpiresAt: record.ExpiresAt}); err != nil {
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

func (s *ReauthService) openReplay(ctx context.Context, tx pgx.Tx, replayID, sessionID uuid.UUID, purpose, key string) (identity.ReauthOutput, error) {
	payload, err := s.idempotency.FindReplayPayloadForUpdate(ctx, tx, replayID)
	if errors.Is(err, idempotency.ErrNotFound) {
		return identity.ReauthOutput{}, deliveryExpired()
	}
	if err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	if payload.Kind != "reauthentication_credential_delivery" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(time.Now().UTC()) || !hmac.Equal(payload.BindingHash, s.replayBinding(sessionID, purpose, key)) {
		return identity.ReauthOutput{}, deliveryExpired()
	}
	var output identity.ReauthOutput
	if err := s.keys.Open(payload.Ciphertext, &output); err != nil || output.Issued.SessionID != sessionID.String() || output.Purpose != purpose {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	if err := s.idempotency.RecordReplay(ctx, tx, replayID); err != nil {
		return identity.ReauthOutput{}, domain.Unavailable()
	}
	return output, nil
}

func (s *ReauthService) reauthRequestHash(purpose, password string) []byte {
	return s.keys.Hash("reauthenticate_email", purpose, password)
}

func (s *ReauthService) replayBinding(sessionID uuid.UUID, purpose, key string) []byte {
	return s.keys.Hash("reauthenticate_email", sessionID.String(), purpose, key)
}

func (s *ReauthService) issueRecoveryTTL() time.Duration {
	if s.recoveryTTL > 0 {
		return s.recoveryTTL
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

func deliveryExpired() error {
	return domain.Problem(410, "AUTH_SESSION_DELIVERY_EXPIRED", "Session credential 전달 복구 시간이 만료되었습니다.")
}

func minReauthTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
func validPurpose(value string) bool { return value == "link_identity" || value == "replace_phone" }
