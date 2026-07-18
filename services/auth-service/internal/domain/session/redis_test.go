package session

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func Test_ParseRedisStatusRecord_parses_allowlisted_fields(t *testing.T) {
	// Given
	userID, sessionID := uuid.New(), uuid.New()
	absoluteExpiry := time.Date(2026, time.July, 17, 13, 0, 0, 0, time.UTC)
	fields := map[string]string{
		"user_id": userID.String(), "session_id": sessionID.String(), "status": "active",
		"idle_expires_at": "", "absolute_expires_at": "1784293200", "status_version": "7", "revoked_until": "",
	}

	// When
	record, err := parseRedisStatusRecord(fields)

	// Then
	require.NoError(t, err)
	require.Equal(t, userID, record.UserID)
	require.Equal(t, sessionID, record.SessionID)
	require.Equal(t, StatusActive, record.State)
	require.Equal(t, absoluteExpiry, record.AbsoluteExpiresAt)
	require.Equal(t, int64(7), record.Version)
}

func Test_ParseRedisStatusRecord_rejects_malformed_cache_data(t *testing.T) {
	// Given
	fields := map[string]string{
		"user_id": uuid.NewString(), "session_id": uuid.NewString(), "status": "unknown",
		"idle_expires_at": "", "absolute_expires_at": "not-a-time", "status_version": "-1", "revoked_until": "",
	}

	// When
	_, err := parseRedisStatusRecord(fields)

	// Then
	require.Error(t, err)
}

func Test_ParseRedisStatusRecord_rejects_token_shaped_unknown_field(t *testing.T) {
	// Given
	fields := validRedisStatusFields()
	fields["access_token"] = "eyJhbGciOiJSUzI1NiJ9.adversarial.signature"

	// When
	_, err := parseRedisStatusRecord(fields)

	// Then
	require.Error(t, err)
}

func Test_ParseRedisStatusRecord_rejects_missing_allowlisted_field(t *testing.T) {
	// Given
	fields := validRedisStatusFields()
	delete(fields, "revoked_until")

	// When
	_, err := parseRedisStatusRecord(fields)

	// Then
	require.Error(t, err)
}

func Test_RedisStatusKey_uses_shared_session_namespace(t *testing.T) {
	// Given
	sessionID := uuid.MustParse("11111111-1111-4111-8111-111111111111")

	// When
	key := RedisStatusKey(sessionID)

	// Then
	require.Equal(t, "auth:session-status:{11111111-1111-4111-8111-111111111111}", key)
}

func validRedisStatusFields() map[string]string {
	return map[string]string{
		"user_id": uuid.NewString(), "session_id": uuid.NewString(), "status": "active",
		"idle_expires_at": "", "absolute_expires_at": "1784293200", "status_version": "7", "revoked_until": "",
	}
}
