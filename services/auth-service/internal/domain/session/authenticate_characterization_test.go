package session

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type activeSessionRepository struct {
	Repository
	session Session
	err     error
}

func (r activeSessionRepository) FindActive(context.Context, uuid.UUID) (Session, error) {
	return r.session, r.err
}

func Test_Service_Authenticate_returns_principal_when_bearer_session_is_active(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	userID, sessionID := uuid.New(), uuid.New()
	keys := characterizationKeys(t, now)
	token, _, err := keys.SignAccessToken(userID.String(), sessionID.String(), time.Minute)
	require.NoError(t, err)
	repository := activeSessionRepository{session: Session{
		ID: sessionID, UserID: userID, Channel: ChannelWeb, Method: "email_password",
		AuthenticatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Status: "active",
	}}
	service := NewService(nil, keys, Config{}, repository, nil, nil, nil)

	// When
	principal, err := service.Authenticate(context.Background(), "", token)

	// Then
	require.NoError(t, err)
	require.True(t, principal.Authenticated)
	require.Equal(t, sessionID, principal.SessionID)
	require.Equal(t, userID, principal.UserID)
}

func Test_Service_Authenticate_rejects_bearer_when_FindActive_returns_not_found(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	userID, sessionID := uuid.New(), uuid.New()
	keys := characterizationKeys(t, now)
	token, _, err := keys.SignAccessToken(userID.String(), sessionID.String(), time.Minute)
	require.NoError(t, err)
	service := NewService(nil, keys, Config{}, activeSessionRepository{err: ErrNotFound}, nil, nil, nil)

	// When
	principal, err := service.Authenticate(context.Background(), "", token)

	// Then
	require.Error(t, err)
	require.False(t, principal.Authenticated)
}

func characterizationKeys(t *testing.T, now time.Time) security.Keys {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	return security.Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}),
		JWTKeyID:       "characterization-key",
		JWTIssuer:      "characterization",
		JWTAudiences:   []string{"dropmong-api"},
		Now:            func() time.Time { return now },
	}
}
