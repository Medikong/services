package worker

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeJob struct{}

func (fakeJob) RunOnce(context.Context) (int, error) { return 0, nil }

func TestGroupStopsWithContext(t *testing.T) {
	state := NewState()
	group, err := NewGroup(
		[]NamedJob{{Name: "fake", Job: fakeJob{}}},
		time.Millisecond,
		state,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, group.Run(ctx))
}

type cancelJob struct {
	started chan struct{}
}

func (j cancelJob) RunOnce(ctx context.Context) (int, error) {
	close(j.started)
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestGroupDoesNotRecordExpectedShutdownAsFailure(t *testing.T) {
	state := NewState()
	var logs bytes.Buffer
	started := make(chan struct{})
	group, err := NewGroup(
		[]NamedJob{{Name: "cancel", Job: cancelJob{started: started}}},
		time.Hour,
		state,
		slog.New(slog.NewTextHandler(&logs, nil)),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- group.Run(ctx) }()
	<-started
	cancel()
	require.NoError(t, <-result)
	require.NoError(t, state.Ready(context.Background()))
	require.NotContains(t, logs.String(), "worker iteration failed")
}
