package usercoupon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUserCouponTerminalLifecycle(t *testing.T) {
	now := time.Now().UTC()
	coupon := Coupon{Status: StatusGranted, ExpiresAt: now.Add(time.Minute), Version: 0}
	_, err := coupon.Expire(now)
	require.ErrorIs(t, err, ErrInvalidTransition)
	expired, err := coupon.Expire(now.Add(2 * time.Minute))
	require.NoError(t, err)
	require.Equal(t, StatusExpired, expired.Status)
}
