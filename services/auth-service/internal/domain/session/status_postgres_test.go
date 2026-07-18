package session

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func Test_StatusRecordFromPostgres_maps_active_session_and_user_to_active(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	row := postgresStatusRow{
		UserID: uuid.New(), SessionID: uuid.New(), SessionState: "active", UserState: "active",
		AbsoluteExpiresAt: now.Add(time.Hour), Version: 4,
	}

	// When
	record := statusRecordFromPostgres(row, 15*time.Minute)

	// Then
	require.Equal(t, StatusActive, record.State)
	require.Nil(t, record.RevokedUntil)
}

func Test_StatusRecordFromPostgres_maps_restricted_user_to_revoked_retention(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	revokedAt := now.Add(-time.Minute)
	row := postgresStatusRow{
		UserID: uuid.New(), SessionID: uuid.New(), SessionState: "active", UserState: "restricted",
		AbsoluteExpiresAt: now.Add(time.Hour), Version: 5, RevokedAt: &revokedAt,
	}

	// When
	record := statusRecordFromPostgres(row, 15*time.Minute)

	// Then
	require.Equal(t, StatusRevoked, record.State)
	require.NotNil(t, record.RevokedUntil)
	require.Equal(t, now.Add(14*time.Minute), *record.RevokedUntil)
}

func Test_StatusRecordFromPostgres_maps_expired_session_to_expired(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	row := postgresStatusRow{
		UserID: uuid.New(), SessionID: uuid.New(), SessionState: "expired", UserState: "active",
		AbsoluteExpiresAt: now.Add(-time.Minute), Version: 6,
	}

	// When
	record := statusRecordFromPostgres(row, 15*time.Minute)

	// Then
	require.Equal(t, StatusExpired, record.State)
}
