package projection

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoverageTracksEveryAuthoritativeEventAndDefersSellerPerformance(t *testing.T) {
	entries := Coverage()
	require.Len(t, entries, 41)
	for index, entry := range entries {
		require.Equal(t, fmt.Sprintf("EVT.A.19-%02d", index+1), entry.EventDocumentID)
		for _, model := range entry.ReadModels {
			require.NotEqual(t, "RM.A.19-03", model)
		}
		if len(entry.ReadModels) == 0 {
			require.NotEmpty(t, entry.Reason, "%s must explain its deliberate no-op projection", entry.EventDocumentID)
		}
	}

	issued, ok := coverage("EVT.A.19-09")
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		RMUserCouponWallet, RMCouponDetails, RMCouponPerformance, RMUserCouponTimeline, RMCouponIncidentStatus,
	}, issued.ReadModels)
	_, ok = coverage("EVT.A.19-42")
	require.False(t, ok)
}

func TestNoOpEventsAreExplicitlyKnown(t *testing.T) {
	registered, ok := coverage("EVT.A.19-01")
	require.True(t, ok)
	require.Empty(t, registered.ReadModels)
	require.NotEmpty(t, registered.Reason)
}
