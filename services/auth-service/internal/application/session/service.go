package session

import (
	"context"
	"crypto/hmac"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	sessiondomain "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	AccessTTL   time.Duration
	RefreshTTL  time.Duration
	SessionTTL  time.Duration
	RecoveryTTL time.Duration
}

type Service struct {
	pool        *pgxpool.Pool
	keys        security.Keys
	config      Config
	sessions    sessiondomain.Repository
	access      access.Repository
	idempotency idempotency.Repository
	outbox      outbox.Repository
}

func NewService(pool *pgxpool.Pool, keys security.Keys, config Config, sessions sessiondomain.Repository, access access.Repository, idempotency idempotency.Repository, outbox outbox.Repository) *Service {
	return &Service{pool: pool, keys: keys, config: config, sessions: sessions, access: access, idempotency: idempotency, outbox: outbox}
}

type Principal struct {
	Authenticated   bool
	SessionID       uuid.UUID
	UserID          uuid.UUID
	Channel         string
	Method          string
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
	Roles           []string
	GrantVersion    int64
}

type TokenSet struct {
	SessionID             string
	UserID                string
	Roles                 []string
	GrantVersion          int64
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
}

type IssueInput struct {
	UserID       uuid.UUID
	IdentityID   uuid.UUID
	IdentityLink uuid.UUID
	Method       string
	Channel      string
	RememberMe   bool
}

type Issued struct {
	TokenSet
	WebCookie string
	CSRFToken string
	ExpiresAt time.Time
}

// RotationInput rotates credentials for an existing Session. Rebind is used
// only after a strong authentication step; a phone replacement rotates the
// credential while retaining the email-rebound Session identity.
type RotationInput struct {
	Principal         Principal
	PreviousWebCookie string
	Rebind            *SessionRebind
}

type SessionRebind struct {
	IdentityID   uuid.UUID
	IdentityLink uuid.UUID
	Method       string
}

// IssueTx creates a channel-specific credential inside the caller's use-case
// transaction. Registration and sign-in keep their own transaction boundary;
// session persistence remains isolated in the session domain repository.
func (s *Service) IssueTx(ctx context.Context, tx pgx.Tx, input IssueInput) (Issued, error) {
	state, grant, err := s.access.FindActiveForUpdate(ctx, tx, input.UserID)
	if errors.Is(err, access.ErrNotFound) || state.Status != "active" {
		return Issued{}, application.Problem(403, "AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 인증을 완료할 수 없습니다.")
	}
	if err != nil {
		return Issued{}, application.Unavailable()
	}
	channel := sessiondomain.Channel(input.Channel)
	if channel != sessiondomain.ChannelWeb && channel != sessiondomain.ChannelMobile {
		return Issued{}, application.Problem(400, "AUTH_INPUT_INVALID", "클라이언트 채널이 올바르지 않습니다.")
	}
	sessionTTL := s.config.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	expiresAt := time.Now().UTC().Add(sessionTTL)
	sessionID := uuid.New()
	credentialID := uuid.New()
	issued := Issued{
		TokenSet: TokenSet{
			SessionID: sessionID.String(), UserID: input.UserID.String(),
			Roles: grant.Roles, GrantVersion: grant.Version,
		},
		ExpiresAt: expiresAt,
	}
	credential := sessiondomain.Credential{ID: credentialID, SessionID: sessionID, ExpiresAt: expiresAt}
	if channel == sessiondomain.ChannelWeb {
		raw, err := s.keys.Opaque("ws_")
		if err != nil {
			return Issued{}, application.Unavailable()
		}
		credential.Type, credential.SecretHash = "web_session_cookie", s.keys.Hash(raw)
		issued.WebCookie, issued.CSRFToken = raw, s.keys.CSRF(credentialID, raw)
	} else {
		raw, err := s.keys.Opaque("rtk_")
		if err != nil {
			return Issued{}, application.Unavailable()
		}
		familyID := uuid.New()
		refreshExpiresAt := time.Now().UTC().Add(s.config.RefreshTTL)
		credential.Type, credential.SecretHash, credential.FamilyID, credential.ExpiresAt = "mobile_refresh_token", s.keys.Hash(raw), &familyID, refreshExpiresAt
		token, accessExpiresAt, err := s.keys.SignAccessToken(input.UserID.String(), sessionID.String(), grant.Roles, grant.Version, s.config.AccessTTL)
		if err != nil {
			return Issued{}, application.Unavailable()
		}
		issued.TokenSet.AccessToken, issued.TokenSet.AccessTokenExpiresAt = token, accessExpiresAt
		issued.TokenSet.RefreshToken, issued.TokenSet.RefreshTokenExpiresAt = raw, refreshExpiresAt
	}
	if err := s.sessions.Create(ctx, tx, sessiondomain.CreateParams{
		Session: sessiondomain.Session{
			ID: sessionID, UserID: input.UserID, IdentityID: input.IdentityID, IdentityLink: input.IdentityLink,
			Method: input.Method, Channel: channel, RememberMe: input.RememberMe, Roles: grant.Roles,
			GrantID: grant.ID, GrantVersion: grant.Version, ExpiresAt: expiresAt,
		},
		Credential: credential,
	}); err != nil {
		return Issued{}, application.Unavailable()
	}
	return issued, nil
}

// RotateForDeliveryTx keeps the Session ID stable and makes the preceding
// credential recovery-only for a short TTL. The caller owns the matching
// idempotency record and encrypted response replay payload.
func (s *Service) RotateForDeliveryTx(ctx context.Context, tx pgx.Tx, input RotationInput) (Issued, error) {
	if !input.Principal.Authenticated || input.Principal.SessionID == uuid.Nil || input.Principal.UserID == uuid.Nil {
		return Issued{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	current, err := s.sessions.FindActiveForUpdate(ctx, tx, input.Principal.SessionID)
	if errors.Is(err, sessiondomain.ErrNotFound) || current.UserID != input.Principal.UserID {
		return Issued{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return Issued{}, application.Unavailable()
	}
	state, grant, err := s.access.FindActiveForUpdate(ctx, tx, current.UserID)
	if errors.Is(err, access.ErrNotFound) || state.Status != "active" {
		return Issued{}, application.Problem(403, "AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 Session을 갱신할 수 없습니다.")
	}
	if err != nil {
		return Issued{}, application.Unavailable()
	}
	if current.Channel != sessiondomain.ChannelWeb && current.Channel != sessiondomain.ChannelMobile {
		return Issued{}, application.Unavailable()
	}
	issued := Issued{TokenSet: TokenSet{SessionID: current.ID.String(), UserID: current.UserID.String(), Roles: grant.Roles, GrantVersion: grant.Version}, ExpiresAt: current.ExpiresAt}
	credentialType := "web_session_cookie"
	if current.Channel == sessiondomain.ChannelMobile {
		credentialType = "mobile_refresh_token"
	}
	previous, err := s.sessions.FindActiveCredentialForUpdate(ctx, tx, current.ID, credentialType)
	if errors.Is(err, sessiondomain.ErrNotFound) {
		return Issued{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return Issued{}, application.Unavailable()
	}
	next := sessiondomain.Credential{ID: uuid.New(), SessionID: current.ID, Type: credentialType}
	if current.Channel == sessiondomain.ChannelWeb {
		if strings.TrimSpace(input.PreviousWebCookie) == "" || !hmac.Equal(previous.SecretHash, s.keys.Hash(input.PreviousWebCookie)) {
			return Issued{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		raw, opaqueErr := s.keys.Opaque("ws_")
		if opaqueErr != nil {
			return Issued{}, application.Unavailable()
		}
		next.SecretHash, next.ExpiresAt = s.keys.Hash(raw), current.ExpiresAt
		issued.WebCookie, issued.CSRFToken = raw, s.keys.CSRF(next.ID, raw)
	} else {
		if previous.FamilyID == nil {
			return Issued{}, application.Unavailable()
		}
		raw, opaqueErr := s.keys.Opaque("rtk_")
		if opaqueErr != nil {
			return Issued{}, application.Unavailable()
		}
		next.FamilyID = previous.FamilyID
		next.ExpiresAt = minExpiry(current.ExpiresAt, time.Now().UTC().Add(s.config.RefreshTTL))
		next.SecretHash = s.keys.Hash(raw)
		accessToken, accessExpiresAt, signErr := s.keys.SignAccessToken(current.UserID.String(), current.ID.String(), grant.Roles, grant.Version, s.config.AccessTTL)
		if signErr != nil {
			return Issued{}, application.Unavailable()
		}
		issued.AccessToken, issued.AccessTokenExpiresAt = accessToken, accessExpiresAt
		issued.RefreshToken, issued.RefreshTokenExpiresAt = raw, next.ExpiresAt
	}
	recoveryExpiresAt := minExpiry(current.ExpiresAt, minExpiry(previous.ExpiresAt, time.Now().UTC().Add(s.recoveryTTL())))
	if err := s.sessions.RotateForDelivery(ctx, tx, previous, next, recoveryExpiresAt); err != nil {
		return Issued{}, application.Unavailable()
	}
	if input.Rebind != nil {
		current.IdentityID = input.Rebind.IdentityID
		current.IdentityLink = input.Rebind.IdentityLink
		current.Method = input.Rebind.Method
		current.GrantID = grant.ID
		current.GrantVersion = grant.Version
		current.Roles = grant.Roles
		if err := s.sessions.Rebind(ctx, tx, current); err != nil {
			return Issued{}, application.Unavailable()
		}
	}
	return issued, nil
}

func (s *Service) Authenticate(ctx context.Context, webCookie, bearer string) (Principal, error) {
	if strings.TrimSpace(webCookie) != "" && strings.TrimSpace(bearer) != "" {
		return Principal{}, application.Problem(400, "AUTH_MULTIPLE_CREDENTIALS", "하나의 인증 수단만 제출할 수 있습니다.")
	}
	if strings.TrimSpace(webCookie) == "" && strings.TrimSpace(bearer) == "" {
		return Principal{Authenticated: false}, nil
	}
	if strings.TrimSpace(bearer) != "" {
		claims, err := s.keys.VerifyAccessToken(bearer)
		if err != nil {
			return Principal{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		sessionID, err := uuid.Parse(claims.SessionID)
		if err != nil {
			return Principal{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		current, err := s.sessions.FindActive(ctx, sessionID)
		if errors.Is(err, sessiondomain.ErrNotFound) {
			return Principal{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		if err != nil {
			return Principal{}, application.Unavailable()
		}
		if current.UserID.String() != claims.Subject || current.GrantVersion != claims.PermissionVersion {
			return Principal{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		return principalFromSession(current), nil
	}
	current, _, err := s.sessions.FindByWebSecret(ctx, s.keys.Hash(webCookie))
	if errors.Is(err, sessiondomain.ErrNotFound) {
		return Principal{}, application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return Principal{}, application.Unavailable()
	}
	return principalFromSession(current), nil
}

func principalFromSession(current sessiondomain.Session) Principal {
	return Principal{
		Authenticated: true, SessionID: current.ID, UserID: current.UserID,
		Channel: string(current.Channel), Method: current.Method, AuthenticatedAt: current.AuthenticatedAt,
		ExpiresAt: current.ExpiresAt, Roles: current.Roles, GrantVersion: current.GrantVersion,
	}
}

func (s *Service) VerifyWebCSRF(ctx context.Context, webCookie, csrfToken string) error {
	if strings.TrimSpace(webCookie) == "" || strings.TrimSpace(csrfToken) == "" {
		return application.Problem(403, "AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	_, credential, err := s.sessions.FindByWebSecret(ctx, s.keys.Hash(webCookie))
	if errors.Is(err, sessiondomain.ErrNotFound) {
		return application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return application.Unavailable()
	}
	expected := s.keys.CSRF(credential.ID, webCookie)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(csrfToken)) != 1 {
		return application.Problem(403, "AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
	}
	return nil
}

func (s *Service) WebCSRF(ctx context.Context, webCookie string) (string, error) {
	if strings.TrimSpace(webCookie) == "" {
		return "", application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	_, credential, err := s.sessions.FindByWebSecret(ctx, s.keys.Hash(webCookie))
	if errors.Is(err, sessiondomain.ErrNotFound) {
		return "", application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return "", application.Unavailable()
	}
	return s.keys.CSRF(credential.ID, webCookie), nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken, idempotencyKey string) (TokenSet, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return TokenSet{}, application.Problem(400, "AUTH_INPUT_INVALID", "refresh token과 Idempotency-Key가 필요합니다.")
	}
	if _, err := uuid.Parse(strings.TrimSpace(idempotencyKey)); err != nil {
		return TokenSet{}, application.Problem(400, "AUTH_INPUT_INVALID", "Idempotency-Key는 UUID여야 합니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, credential, err := s.sessions.FindByRefreshSecretForUpdate(ctx, tx, s.keys.Hash(refreshToken))
	if errors.Is(err, sessiondomain.ErrNotFound) {
		return TokenSet{}, application.Problem(401, "AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
	}
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	scopeHash := s.keys.Hash("mobile_refresh", current.ID.String())
	keyHash := s.keys.Hash(idempotencyKey)
	requestHash := s.keys.Hash("mobile_refresh", refreshToken)
	record, recordErr := s.idempotency.FindForUpdate(ctx, tx, "mobile_refresh", scopeHash, keyHash)
	if recordErr == nil {
		if !hmac.Equal(record.RequestHash, requestHash) {
			return TokenSet{}, application.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
		if current.Status != "active" {
			return TokenSet{}, application.Problem(401, "AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
		}
		state, _, stateErr := s.access.FindActiveForUpdate(ctx, tx, current.UserID)
		if errors.Is(stateErr, access.ErrNotFound) || state.Status != "active" {
			if record.ReplayID != nil {
				_ = s.idempotency.DestroyReplayPayload(ctx, tx, *record.ReplayID)
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return TokenSet{}, application.Unavailable()
			}
			return TokenSet{}, application.Problem(403, "AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 Session을 갱신할 수 없습니다.")
		}
		if stateErr != nil {
			return TokenSet{}, application.Unavailable()
		}
		if credential.Status == "rotated" && record.Status == "completed" && record.ReplayID != nil {
			return s.replayRefresh(ctx, tx, current, record, s.keys.Hash("mobile_refresh_replay", current.ID.String(), idempotencyKey, refreshToken))
		}
		return TokenSet{}, application.Unavailable()
	}
	if !errors.Is(recordErr, idempotency.ErrNotFound) {
		return TokenSet{}, application.Unavailable()
	}
	if credential.Status == "rotated_pending_delivery" {
		return TokenSet{}, application.Problem(401, "AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
	}
	if credential.Status != "active" || current.Status != "active" || !credential.ExpiresAt.After(time.Now()) {
		if credential.FamilyID != nil {
			if err := s.sessions.MarkReuseDetected(ctx, tx, current.ID, *credential.FamilyID); err != nil {
				return TokenSet{}, application.Unavailable()
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenSet{}, application.Unavailable()
		}
		return TokenSet{}, application.Problem(401, "AUTH_SESSION_REVOKED", "Session을 갱신할 수 없습니다.")
	}
	state, grant, err := s.access.FindActiveForUpdate(ctx, tx, current.UserID)
	if errors.Is(err, access.ErrNotFound) || state.Status != "active" {
		return TokenSet{}, application.Problem(403, "AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 Session을 갱신할 수 없습니다.")
	}
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	rawRefresh, err := s.keys.Opaque("rtk_")
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	familyID := credential.FamilyID
	if familyID == nil {
		return TokenSet{}, application.Unavailable()
	}
	nextExpiry := time.Now().UTC().Add(s.config.RefreshTTL)
	next := sessiondomain.Credential{
		ID: uuid.New(), SessionID: current.ID, Type: "mobile_refresh_token",
		SecretHash: s.keys.Hash(rawRefresh), FamilyID: familyID, ExpiresAt: nextExpiry,
	}
	if err := s.sessions.RotateRefresh(ctx, tx, credential, next); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	accessToken, accessExpiresAt, err := s.keys.SignAccessToken(current.UserID.String(), current.ID.String(), grant.Roles, grant.Version, s.config.AccessTTL)
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	result := TokenSet{
		SessionID: current.ID.String(), UserID: current.UserID.String(), Roles: grant.Roles,
		GrantVersion: grant.Version, AccessToken: accessToken, AccessTokenExpiresAt: accessExpiresAt,
		RefreshToken: rawRefresh, RefreshTokenExpiresAt: nextExpiry,
	}
	ciphertext, err := s.keys.Seal(result)
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	replayID := uuid.New()
	retryExpiresAt := minExpiry(time.Now().UTC().Add(s.recoveryTTL()), nextExpiry)
	bindingHash := s.keys.Hash("mobile_refresh_replay", current.ID.String(), idempotencyKey, refreshToken)
	if err := s.idempotency.CreateReplayPayload(ctx, tx, idempotency.ReplayPayload{
		ID: replayID, Kind: "mobile_refresh_response", Ciphertext: ciphertext, BindingHash: bindingHash, ExpiresAt: retryExpiresAt,
	}); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	if err := s.idempotency.CreateCompleted(ctx, tx, idempotency.NewRecord(
		"mobile_refresh", scopeHash, keyHash, requestHash, &current.ID, &replayID, retryExpiresAt,
	), "Session", "rotated"); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	if err := s.outbox.Append(ctx, tx, outbox.Event{
		ID: uuid.New(), Type: "Auth.SessionRefreshRotated", AggregateType: "Session", AggregateID: current.ID,
		Version: 0, Payload: json.RawMessage(`{"status":"rotated"}`), CorrelationID: current.ID,
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	if err := application.AppendAudit(ctx, tx, "auth.session.refresh_rotated", "user", current.UserID, current.ID, map[string]string{"channel": "mobile"}, idempotencyKey); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	return result, nil
}

func (s *Service) replayRefresh(ctx context.Context, tx pgx.Tx, current sessiondomain.Session, record idempotency.Record, expectedBinding []byte) (TokenSet, error) {
	payload, err := s.idempotency.FindReplayPayloadForUpdate(ctx, tx, *record.ReplayID)
	if errors.Is(err, idempotency.ErrNotFound) {
		return TokenSet{}, application.Problem(410, "AUTH_REFRESH_RETRY_EXPIRED", "refresh 재시도 가능 시간이 만료되었습니다.")
	}
	if err != nil {
		return TokenSet{}, application.Unavailable()
	}
	if payload.Kind != "mobile_refresh_response" || !hmac.Equal(payload.BindingHash, expectedBinding) || payload.DestroyedAt != nil || !payload.ExpiresAt.After(time.Now()) {
		if err := s.idempotency.DestroyReplayPayload(ctx, tx, payload.ID); err != nil {
			return TokenSet{}, application.Unavailable()
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenSet{}, application.Unavailable()
		}
		return TokenSet{}, application.Problem(410, "AUTH_REFRESH_RETRY_EXPIRED", "refresh 재시도 가능 시간이 만료되었습니다.")
	}
	var result TokenSet
	if err := s.keys.Open(payload.Ciphertext, &result); err != nil || result.SessionID != current.ID.String() {
		return TokenSet{}, application.Unavailable()
	}
	if err := s.idempotency.RecordReplay(ctx, tx, payload.ID); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenSet{}, application.Unavailable()
	}
	return result, nil
}

func minExpiry(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *Service) recoveryTTL() time.Duration {
	if s.config.RecoveryTTL > 0 {
		return s.config.RecoveryTTL
	}
	return 5 * time.Minute
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	if !principal.Authenticated {
		return application.Problem(401, "AUTH_SESSION_REQUIRED", "로그인 상태가 필요합니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := s.sessions.Revoke(ctx, tx, principal.SessionID, "logout"); err != nil {
		return application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return application.Unavailable()
	}
	return nil
}

// LogoutByRefresh handles the mobile contract, whose current refresh token is
// the credential that authorizes logout rather than an access JWT.
func (s *Service) LogoutByRefresh(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	current, _, err := s.sessions.FindByRefreshSecretForUpdate(ctx, tx, s.keys.Hash(refreshToken))
	if errors.Is(err, sessiondomain.ErrNotFound) {
		return application.Problem(401, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return application.Unavailable()
	}
	if err := s.sessions.Revoke(ctx, tx, current.ID, "logout"); err != nil {
		return application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return application.Unavailable()
	}
	return nil
}
