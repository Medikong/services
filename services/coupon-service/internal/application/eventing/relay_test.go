package eventing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackoffIsBounded(t *testing.T) {
	require.Equal(t, time.Second, RetryDelay(1, time.Second, time.Minute))
	require.Equal(t, 8*time.Second, RetryDelay(4, time.Second, time.Minute))
	require.Equal(t, time.Minute, RetryDelay(100, time.Second, time.Minute))
}
