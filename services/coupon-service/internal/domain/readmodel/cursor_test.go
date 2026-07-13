package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTripAndKindIsolation(t *testing.T) {
	at := time.Date(2026, 7, 12, 9, 30, 0, 123, time.UTC)
	id := "00000000-0000-0000-0000-000000000001"
	encoded, err := encodeCursor("timeline", at, id)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), maxCursorLength)

	gotAt, gotID, err := decodeCursor(encoded, "timeline")
	require.NoError(t, err)
	require.Equal(t, at, gotAt)
	require.Equal(t, id, gotID)

	_, _, err = decodeCursor(encoded, "wallet")
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, _, err = decodeCursor(encoded+"!", "timeline")
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, _, err = decodeCursor(strings.Repeat("x", maxCursorLength+1), "timeline")
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestCursorRejectsUnknownFieldsAndTrailingDocuments(t *testing.T) {
	unknown := "eyJ2IjoxLCJrIjoid2FsbGV0IiwidCI6IjIwMjYtMDctMTJUMDA6MDA6MDBaIiwiaWQiOiJpZCIsIngiOnRydWV9"
	_, _, err := decodeCursor(unknown, "wallet")
	require.ErrorIs(t, err, ErrInvalidCursor)

	trailing := "eyJ2IjoxLCJrIjoid2FsbGV0IiwidCI6IjIwMjYtMDctMTJUMDA6MDA6MDBaIiwiaWQiOiJpZCJ9e30"
	_, _, err = decodeCursor(trailing, "wallet")
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestPagesUseLastReturnedItemAsCursor(t *testing.T) {
	at := time.Date(2026, 7, 12, 9, 30, 0, 0, time.UTC)
	page, err := timelinePage([]TimelineEvent{
		{TimelineID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), OccurredAt: at.Add(2 * time.Minute)},
		{TimelineID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), OccurredAt: at.Add(time.Minute)},
		{TimelineID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), OccurredAt: at},
	}, 2)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.NotEmpty(t, page.NextCursor)

	cursorAt, cursorID, err := decodeCursor(page.NextCursor, "timeline")
	require.NoError(t, err)
	require.Equal(t, at.Add(time.Minute), cursorAt)
	require.Equal(t, "00000000-0000-0000-0000-000000000002", cursorID)
}

func TestNormalizeLimitEnforcesOpenAPIBounds(t *testing.T) {
	value, err := normalizeLimit(0)
	require.NoError(t, err)
	require.Equal(t, DefaultLimit, value)
	_, err = normalizeLimit(-1)
	require.ErrorIs(t, err, ErrInvalidQuery)
	_, err = normalizeLimit(MaxLimit + 1)
	require.ErrorIs(t, err, ErrInvalidQuery)
}
