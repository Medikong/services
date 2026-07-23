package session

import (
	"context"
	"errors"
	"strings"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
)

func (s *Service) Issue(ctx context.Context, input IssueInput) (Issued, error) {
	var issued Issued
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		var err error
		issued, err = s.IssueTx(ctx, repositories, input)
		return err
	})
	if err != nil {
		return Issued{}, unavailable(err)
	}
	return issued, nil
}

// IssueTx participates in a transaction whose repositories were bound by infrastructure.
func (s *Service) IssueTx(ctx context.Context, repositories TxRepositories, input IssueInput) (Issued, error) {
	state, err := repositories.UserAuthState.FindForUpdate(ctx, input.UserID)
	if errors.Is(err, domainuserauthstate.ErrNotFound) || (err == nil && state.Status != domainuserauthstate.StatusActive) {
		return Issued{}, forbidden("AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 인증을 완료할 수 없습니다.")
	}
	if err != nil {
		return Issued{}, unavailable(err)
	}

	channel := domainsession.Channel(input.Channel)
	if channel != domainsession.ChannelWeb && channel != domainsession.ChannelIOS && channel != domainsession.ChannelAndroid {
		return Issued{}, invalid("AUTH_INPUT_INVALID", "클라이언트 채널이 올바르지 않습니다.")
	}
	sessionTTL := s.config.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	if input.RememberMe && s.config.RememberMeSessionTTL > 0 {
		sessionTTL = s.config.RememberMeSessionTTL
	}
	if input.SessionTTLOverride > 0 {
		sessionTTL = input.SessionTTLOverride
	}
	if channel != domainsession.ChannelWeb && s.config.RefreshTTL > sessionTTL {
		sessionTTL = s.config.RefreshTTL
	}

	issuedAt := s.clock.Now().UTC()
	expiresAt := issuedAt.Add(sessionTTL)
	sessionID := uuid.New()
	credentialID := uuid.New()
	issued := Issued{
		TokenSet: TokenSet{
			SessionID: sessionID.String(), UserID: input.UserID.String(), Channel: string(channel), SessionExpiresAt: expiresAt,
		},
		ExpiresAt: expiresAt, RememberMe: input.RememberMe,
	}
	credential := domainsession.Credential{ID: credentialID, SessionID: sessionID, ExpiresAt: expiresAt}
	accessTTL := s.config.AccessTTL
	if input.AccessTTLOverride > 0 {
		accessTTL = input.AccessTTLOverride
	}
	accessToken, accessExpiresAt, err := s.cryptography.SignAccessToken(input.UserID, sessionID, accessTTL)
	if err != nil {
		return Issued{}, unavailable(err)
	}
	issued.AccessToken, issued.AccessTokenExpiresAt = accessToken, accessExpiresAt

	raw, err := s.cryptography.Opaque("rtk_")
	if err != nil {
		return Issued{}, unavailable(err)
	}
	familyID := uuid.New()
	if channel == domainsession.ChannelWeb {
		if strings.TrimSpace(input.WebCSRFToken) == "" {
			return Issued{}, forbidden("AUTH_CSRF_INVALID", "CSRF 검증에 실패했습니다.")
		}
		refreshExpiresAt := minExpiry(expiresAt, issuedAt.Add(s.config.RefreshTTL))
		credential.Type = "web_refresh_cookie"
		credential.SecretHash = s.cryptography.Hash(raw)
		credential.CSRFHash = s.cryptography.Hash("csrf", input.WebCSRFToken)
		credential.FamilyID = &familyID
		credential.ExpiresAt = refreshExpiresAt
		issued.WebCookie, issued.CSRFToken = raw, input.WebCSRFToken
		issued.RefreshTokenExpiresAt = refreshExpiresAt
	} else {
		refreshExpiresAt := issuedAt.Add(s.config.RefreshTTL)
		credential.Type = "mobile_refresh_token"
		credential.SecretHash = s.cryptography.Hash(raw)
		credential.FamilyID = &familyID
		credential.ExpiresAt = refreshExpiresAt
		issued.RefreshToken, issued.RefreshTokenExpiresAt = raw, refreshExpiresAt
	}

	if err := repositories.Sessions.Create(ctx, domainsession.CreateParams{
		Session: domainsession.Session{
			ID: sessionID, UserID: input.UserID, IdentityID: input.IdentityID, IdentityLink: input.IdentityLink,
			Method: input.Method, Channel: channel, RememberMe: input.RememberMe, ExpiresAt: expiresAt,
		},
		Credential: credential,
	}); err != nil {
		return Issued{}, unavailable(err)
	}
	return issued, nil
}

func (s *Service) RotateForDelivery(ctx context.Context, input RotationInput) (Issued, error) {
	var issued Issued
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		var err error
		issued, err = s.RotateForDeliveryTx(ctx, repositories, input)
		return err
	})
	if err != nil {
		return Issued{}, unavailable(err)
	}
	return issued, nil
}

// RotateForDeliveryTx keeps the Session ID stable inside a caller-owned transaction.
func (s *Service) RotateForDeliveryTx(ctx context.Context, repositories TxRepositories, input RotationInput) (Issued, error) {
	if !input.Principal.Authenticated || input.Principal.SessionID == uuid.Nil || input.Principal.UserID == uuid.Nil {
		return Issued{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	current, err := repositories.Sessions.FindActiveForUpdate(ctx, input.Principal.SessionID)
	if errors.Is(err, domainsession.ErrNotFound) || (err == nil && current.UserID != input.Principal.UserID) {
		return Issued{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return Issued{}, unavailable(err)
	}
	state, err := repositories.UserAuthState.FindForUpdate(ctx, current.UserID)
	if errors.Is(err, domainuserauthstate.ErrNotFound) || (err == nil && state.Status != domainuserauthstate.StatusActive) {
		return Issued{}, forbidden("AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 Session을 갱신할 수 없습니다.")
	}
	if err != nil {
		return Issued{}, unavailable(err)
	}
	if current.Channel != domainsession.ChannelWeb && current.Channel != domainsession.ChannelIOS && current.Channel != domainsession.ChannelAndroid {
		return Issued{}, unavailable(nil)
	}

	issued := Issued{
		TokenSet: TokenSet{
			SessionID: current.ID.String(), UserID: current.UserID.String(), Channel: string(current.Channel), SessionExpiresAt: current.ExpiresAt,
		},
		ExpiresAt: current.ExpiresAt, RememberMe: current.RememberMe,
	}
	credentialType := "web_refresh_cookie"
	if current.Channel != domainsession.ChannelWeb {
		credentialType = "mobile_refresh_token"
	}
	previous, err := repositories.Sessions.FindActiveCredentialForUpdate(ctx, current.ID, credentialType)
	if errors.Is(err, domainsession.ErrNotFound) {
		return Issued{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
	}
	if err != nil {
		return Issued{}, unavailable(err)
	}

	next := domainsession.Credential{ID: uuid.New(), SessionID: current.ID, Type: credentialType}
	now := s.clock.Now().UTC()
	if current.Channel == domainsession.ChannelWeb {
		if strings.TrimSpace(input.PreviousWebCookie) != "" && !s.cryptography.Equal(previous.SecretHash, input.PreviousWebCookie) {
			return Issued{}, unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다.")
		}
		raw, opaqueErr := s.cryptography.Opaque("rtk_")
		if opaqueErr != nil {
			return Issued{}, unavailable(opaqueErr)
		}
		next.SecretHash, next.CSRFHash, next.FamilyID, next.ExpiresAt = s.cryptography.Hash(raw), previous.CSRFHash, previous.FamilyID, previous.ExpiresAt
		issued.WebCookie, issued.RefreshTokenExpiresAt = raw, next.ExpiresAt
	} else {
		if previous.FamilyID == nil {
			return Issued{}, unavailable(nil)
		}
		raw, opaqueErr := s.cryptography.Opaque("rtk_")
		if opaqueErr != nil {
			return Issued{}, unavailable(opaqueErr)
		}
		next.FamilyID = previous.FamilyID
		next.ExpiresAt = minExpiry(current.ExpiresAt, now.Add(s.config.RefreshTTL))
		next.SecretHash = s.cryptography.Hash(raw)
		issued.RefreshToken, issued.RefreshTokenExpiresAt = raw, next.ExpiresAt
	}

	accessToken, accessExpiresAt, err := s.cryptography.SignAccessToken(current.UserID, current.ID, s.config.AccessTTL)
	if err != nil {
		return Issued{}, unavailable(err)
	}
	issued.AccessToken, issued.AccessTokenExpiresAt = accessToken, accessExpiresAt
	recoveryExpiresAt := minExpiry(current.ExpiresAt, minExpiry(previous.ExpiresAt, now.Add(s.recoveryTTL())))
	if err := repositories.Sessions.RotateForDelivery(ctx, previous, next, recoveryExpiresAt); err != nil {
		return Issued{}, unavailable(err)
	}
	if input.Rebind != nil {
		current.IdentityID = input.Rebind.IdentityID
		current.IdentityLink = input.Rebind.IdentityLink
		current.Method = input.Rebind.Method
		if err := repositories.Sessions.Rebind(ctx, current); err != nil {
			return Issued{}, unavailable(err)
		}
	}
	return issued, nil
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
