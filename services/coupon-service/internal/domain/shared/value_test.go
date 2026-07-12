package shared

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMoneyAndSnapshotValidation(t *testing.T) {
	require.NoError(t, (Money{Amount: "5000", Currency: "KRW"}).Validate())
	require.Error(t, (Money{Amount: "-1", Currency: "KRW"}).Validate())
	require.NoError(t, (SnapshotRef{
		SourceRef:     ExternalRef{Context: "order", Type: "order", ID: "order:123"},
		SourceVersion: "1",
		CapturedAt:    time.Now(),
		PayloadHash:   "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}).Validate())
}
