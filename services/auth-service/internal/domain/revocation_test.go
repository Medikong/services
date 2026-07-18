package domain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingRevocationRollback struct {
	steps       *[]string
	contextLive *bool
}

func (r recordingRevocationRollback) Rollback(ctx context.Context) error {
	*r.contextLive = ctx.Err() == nil
	*r.steps = append(*r.steps, "rollback")
	return nil
}

type recordingRevocationFences struct {
	steps       *[]string
	contextLive *bool
}

func (f recordingRevocationFences) Resolve(ctx context.Context) error {
	*f.contextLive = ctx.Err() == nil
	*f.steps = append(*f.steps, "resolve")
	return nil
}

func Test_ResolveRevocationRollback_releases_transaction_before_restoring_fence(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	steps := make([]string, 0, 2)
	rollbackContextLive, resolutionContextLive := false, false

	// When
	ResolveRevocationRollback(
		ctx,
		recordingRevocationRollback{steps: &steps, contextLive: &rollbackContextLive},
		recordingRevocationFences{steps: &steps, contextLive: &resolutionContextLive},
	)

	// Then
	require.Equal(t, []string{"rollback", "resolve"}, steps)
	require.True(t, rollbackContextLive)
	require.True(t, resolutionContextLive)
}
