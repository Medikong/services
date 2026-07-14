package couponcode

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFingerprintDoesNotExposeRawCode(t *testing.T) {
	digest, suffix, err := Fingerprint(" abcd-1234 ", []byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	require.Len(t, digest, 32)
	require.Equal(t, "1234", suffix)
}

func TestFingerprintCountsUnicodeCodePoints(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	_, suffix, err := Fingerprint(strings.Repeat("가", 128), key)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("가", 4), suffix)
	_, _, err = Fingerprint(strings.Repeat("가", 129), key)
	require.Error(t, err)
}

func TestCodeReservationLifecycle(t *testing.T) {
	now := time.Now().UTC()
	code := Code{Status: CodeAvailable}
	reserved, err := code.Reserve("issue-1", now.Add(time.Minute), now)
	require.NoError(t, err)
	replayed, err := reserved.Reserve("issue-1", now.Add(2*time.Minute), now)
	require.NoError(t, err)
	require.Equal(t, reserved, replayed)
	_, err = reserved.Reserve("issue-2", now.Add(time.Minute), now)
	require.ErrorIs(t, err, ErrCodeUnavailable)
	redeemed, err := reserved.Redeem("issue-1", "user-coupon-1", now)
	require.NoError(t, err)
	require.Equal(t, CodeRedeemed, redeemed.Status)
	_, err = redeemed.Release("issue-1")
	require.ErrorIs(t, err, ErrInvalidTransition)
}
