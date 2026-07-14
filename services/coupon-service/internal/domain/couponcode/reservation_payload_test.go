package couponcode

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReservationPayloadCarriesUserCorrelationWithoutCodeSource(t *testing.T) {
	reservedUntil := time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC)
	payload := reservationPayload(Code{
		ID: "code-1", BatchID: "batch-1", CampaignID: "campaign-1", Status: CodeReserved,
		ReservedIssueRequestID: "issue-1", ReservedUntil: &reservedUntil,
	}, "user-1")

	var event map[string]any
	require.NoError(t, json.Unmarshal(payload, &event))
	require.Equal(t, "user-1", event["userId"])
	require.Equal(t, "issue-1", event["issueRequestId"])
	require.Equal(t, "campaign-1", event["campaignId"])
	require.NotContains(t, string(payload), "ABCD-1234")
}
