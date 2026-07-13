package campaign

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"
	"github.com/stretchr/testify/require"
)

type redisEvalFake struct {
	redis.Cmdable
	result any
	err    error
	script string
	keys   []string
	args   []any
}

func (f *redisEvalFake) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	f.script = script
	f.keys = keys
	f.args = args
	cmd := redis.NewCmd(ctx)
	cmd.SetVal(f.result)
	cmd.SetErr(f.err)
	return cmd
}

func TestRedisGateReturnsOnlyAnAdvisoryRejection(t *testing.T) {
	client := &redisEvalFake{result: []any{int64(0), int64(0), int64(9), int64(1)}}
	gate, err := NewRedisGate(client, "coupon", time.Minute)
	require.NoError(t, err)

	result, err := gate.Admit(context.Background(), "camp_abcdefgh", "ireq_abcdefgh", 2, 10)
	require.NoError(t, err)
	require.True(t, result.Rejected())
	require.EqualValues(t, 9, result.Used)
	require.EqualValues(t, 1, result.Remaining)
	require.Contains(t, client.script, "used + quantity > capacity")
	require.Equal(t, []string{"coupon:v1:campaign:camp_abcdefgh:quantity"}, client.keys)
	require.Equal(t, []any{"request:ireq_abcdefgh", int64(2), int64(10), int64(time.Minute.Milliseconds())}, client.args)
}

func TestRedisGateSurfacesTechnicalFailureInsteadOfBusinessRejection(t *testing.T) {
	client := &redisEvalFake{err: oops.In("test").Code("test.redis_down").New("redis is unavailable")}
	gate, err := NewRedisGate(client, "coupon", time.Minute)
	require.NoError(t, err)

	result, err := gate.Admit(context.Background(), "camp_abcdefgh", "ireq_abcdefgh", 1, 10)
	require.Error(t, err)
	require.False(t, result.Rejected())
}

func TestRedisGateRejectsMissingAndConflictingState(t *testing.T) {
	client := &redisEvalFake{result: []any{int64(-1), int64(0), int64(0), int64(0)}}
	gate, err := NewRedisGate(client, "coupon", time.Minute)
	require.NoError(t, err)

	_, err = gate.Complete(context.Background(), "camp_abcdefgh", "ireq_abcdefgh", 1)
	require.ErrorIs(t, err, ErrGateStateMissing)

	client.result = []any{int64(-2), int64(0), int64(1), int64(9)}
	_, err = gate.Compensate(context.Background(), "camp_abcdefgh", "ireq_abcdefgh", 2)
	require.ErrorIs(t, err, ErrGateStateConflict)
}
