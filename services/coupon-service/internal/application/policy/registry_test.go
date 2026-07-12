package policy

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyCatalogTracksAllAuthoritativePolicies(t *testing.T) {
	definitions := Definitions()
	require.Len(t, definitions, 22)
	for index, definition := range definitions {
		require.Equal(t, fmt.Sprintf("POLICY.A.19-%02d", index+1), definition.ID)
		require.NotEmpty(t, definition.Name)
	}
}

func TestUnresolvedPoliciesDoNotEnqueueCommands(t *testing.T) {
	for _, route := range EventRoutes("EVT.A.19-31") {
		require.NotEqual(t, "CMD.A.19-12", route.Route.CommandID)
	}
}

func TestEventStringAcceptsRepositoryAndContractFieldNames(t *testing.T) {
	for name, data := range map[string]map[string]any{
		"contract":   {"issue_request_id": "ireq_contract"},
		"repository": {"issueRequestId": "ireq_repository"},
	} {
		t.Run(name, func(t *testing.T) {
			value, ok := eventString(data, "issue_request_id")
			require.True(t, ok)
			require.NotEmpty(t, value)
		})
	}
	_, ok := eventString(map[string]any{"campaignId": ""}, "campaign_id")
	require.False(t, ok)
}

func TestTerminalCodeIssueOutcomesRequestReservationRelease(t *testing.T) {
	for _, eventID := range []string{"EVT.A.19-08", "EVT.A.19-11"} {
		t.Run(eventID, func(t *testing.T) {
			var found bool
			for _, match := range EventRoutes(eventID) {
				if match.Route.CommandID == "CMD.A.19-17" && match.Route.TargetIDField == "issue_request_id" {
					found = true
				}
			}
			require.True(t, found, "%s must request CMD.A.19-17", eventID)
		})
	}
}

func TestRecoveryReplayIsOwnedByRecoveryWorker(t *testing.T) {
	definitions := Definitions()
	require.Equal(t, ModeWorker, definitions[20].Mode)
	for _, match := range EventRoutes("EVT.A.19-39") {
		require.NotEqual(t, "CMD.A.19-32", match.Route.CommandID)
	}
}

func TestIssueExecutionIsOwnedByIssueWorker(t *testing.T) {
	definitions := Definitions()
	require.Equal(t, ModeWorker, definitions[8].Mode)
	for _, match := range EventRoutes("EVT.A.19-36") {
		require.NotEqual(t, "CMD.A.19-07", match.Route.CommandID)
	}
}

func TestRetryPolicyRequiresScheduledRetryTime(t *testing.T) {
	require.True(t, eventHasRetryTime(map[string]any{"nextAttemptAt": "2026-07-12T10:00:00Z"}))
	require.True(t, eventHasRetryTime(map[string]any{"next_attempt_at": "2026-07-12T10:00:00Z"}))
	require.False(t, eventHasRetryTime(map[string]any{"nextAttemptAt": nil}))
	require.False(t, eventHasRetryTime(map[string]any{}))
}
