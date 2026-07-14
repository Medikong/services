package campaign

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

type GateSignal string

const (
	GateAdmitted    GateSignal = "admitted"
	GateRejected    GateSignal = "rejected"
	GateCompleted   GateSignal = "completed"
	GateCompensated GateSignal = "compensated"
)

// GateResult is an admission hint, not an issuance decision. A result that is
// not rejected must still be finalized by ReserveQuantity in Postgres.
type GateResult struct {
	Signal    GateSignal
	Replayed  bool
	Used      int64
	Remaining int64
}

func (r GateResult) Rejected() bool {
	return r.Signal == GateRejected
}

type RedisGate struct {
	client    redis.Cmdable
	keyPrefix string
	ttl       time.Duration
}

func NewRedisGate(client redis.Cmdable, keyPrefix string, ttl time.Duration) (*RedisGate, error) {
	if client == nil || strings.TrimSpace(keyPrefix) == "" || ttl <= 0 {
		return nil, oops.In("coupon_quantity_gate").Code("quantity_gate.config_invalid").New("redis client, key prefix, and positive ttl are required")
	}
	return &RedisGate{client: client, keyPrefix: strings.TrimSuffix(keyPrefix, ":"), ttl: ttl}, nil
}

func (g *RedisGate) Admit(ctx context.Context, campaignID, issueRequestID string, quantity, capacity int64) (GateResult, error) {
	if strings.TrimSpace(campaignID) == "" || strings.TrimSpace(issueRequestID) == "" || quantity <= 0 || capacity < 0 {
		return GateResult{}, ErrInvalidQuantity
	}
	return g.run(ctx, admitScript, campaignID, issueRequestID, quantity, capacity)
}

func (g *RedisGate) Complete(ctx context.Context, campaignID, issueRequestID string, quantity int64) (GateResult, error) {
	if strings.TrimSpace(campaignID) == "" || strings.TrimSpace(issueRequestID) == "" || quantity <= 0 {
		return GateResult{}, ErrInvalidQuantity
	}
	return g.run(ctx, completeScript, campaignID, issueRequestID, quantity)
}

func (g *RedisGate) Compensate(ctx context.Context, campaignID, issueRequestID string, quantity int64) (GateResult, error) {
	if strings.TrimSpace(campaignID) == "" || strings.TrimSpace(issueRequestID) == "" || quantity <= 0 {
		return GateResult{}, ErrInvalidQuantity
	}
	return g.run(ctx, compensateScript, campaignID, issueRequestID, quantity)
}

func (g *RedisGate) run(ctx context.Context, script, campaignID, issueRequestID string, values ...any) (GateResult, error) {
	args := []any{"request:" + issueRequestID}
	args = append(args, values...)
	args = append(args, g.ttl.Milliseconds())
	value, err := g.client.Eval(ctx, script, []string{g.key(campaignID)}, args...).Result()
	if err != nil {
		return GateResult{}, oops.In("coupon_quantity_gate").Code("quantity_gate.redis_failed").Wrap(err)
	}
	return parseGateResult(value)
}

func (g *RedisGate) key(campaignID string) string {
	return strings.Join([]string{g.keyPrefix, "v1", "campaign", campaignID, "quantity"}, ":")
}

func parseGateResult(value any) (GateResult, error) {
	values, ok := value.([]any)
	if !ok || len(values) != 4 {
		return GateResult{}, oops.In("coupon_quantity_gate").Code("quantity_gate.response_invalid").New("redis gate returned an invalid response")
	}
	code, err := gateInt(values[0])
	if err != nil {
		return GateResult{}, err
	}
	replayed, err := gateInt(values[1])
	if err != nil {
		return GateResult{}, err
	}
	used, err := gateInt(values[2])
	if err != nil {
		return GateResult{}, err
	}
	remaining, err := gateInt(values[3])
	if err != nil {
		return GateResult{}, err
	}
	result := GateResult{Replayed: replayed == 1, Used: used, Remaining: remaining}
	switch code {
	case 0:
		result.Signal = GateRejected
	case 1:
		result.Signal = GateAdmitted
	case 2:
		result.Signal = GateCompleted
	case 3:
		result.Signal = GateCompensated
	case -1:
		return GateResult{}, ErrGateStateMissing
	case -2:
		return GateResult{}, ErrGateStateConflict
	default:
		return GateResult{}, oops.In("coupon_quantity_gate").Code("quantity_gate.response_invalid").New("redis gate returned an unknown state")
	}
	return result, nil
}

func gateInt(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, oops.In("coupon_quantity_gate").Code("quantity_gate.response_invalid").Wrap(err)
		}
		return parsed, nil
	case []byte:
		return gateInt(string(value))
	default:
		return 0, oops.In("coupon_quantity_gate").Code("quantity_gate.response_invalid").New("redis gate returned a non-integer value")
	}
}

var (
	ErrGateStateMissing  = oops.In("coupon_quantity_gate").Code("quantity_gate.state_missing").New("redis admission state is missing")
	ErrGateStateConflict = oops.In("coupon_quantity_gate").Code("quantity_gate.state_conflict").New("redis admission state conflicts with the request")
)

const admitScript = `
local field = ARGV[1]
local quantity = tonumber(ARGV[2])
local capacity = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local current = redis.call('HGET', KEYS[1], field)
local used = tonumber(redis.call('HGET', KEYS[1], 'used') or '0')

local function remaining()
  local value = capacity - used
  if value < 0 then return 0 end
  return value
end

if current then
  local separator = string.find(current, '|', 1, true)
  if not separator then return {-2, 1, used, remaining()} end
  local state = string.sub(current, 1, separator - 1)
  local stored = tonumber(string.sub(current, separator + 1))
  if stored ~= quantity then return {-2, 1, used, remaining()} end
  if state == 'admitted' then return {1, 1, used, remaining()} end
  if state == 'completed' then return {2, 1, used, remaining()} end
  if state ~= 'compensated' then return {-2, 1, used, remaining()} end
end

if used + quantity > capacity then
  redis.call('HSET', KEYS[1], 'capacity', capacity)
  redis.call('PEXPIRE', KEYS[1], ttl)
  return {0, 0, used, remaining()}
end

used = used + quantity
redis.call('HSET', KEYS[1], 'used', used, 'capacity', capacity, field, 'admitted|' .. quantity)
redis.call('PEXPIRE', KEYS[1], ttl)
return {1, 0, used, remaining()}
`

const completeScript = `
local field = ARGV[1]
local quantity = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local current = redis.call('HGET', KEYS[1], field)
local used = tonumber(redis.call('HGET', KEYS[1], 'used') or '0')
local capacity = tonumber(redis.call('HGET', KEYS[1], 'capacity') or used)
local remaining = capacity - used
if remaining < 0 then remaining = 0 end
if not current then return {-1, 0, used, remaining} end
local separator = string.find(current, '|', 1, true)
if not separator then return {-2, 0, used, remaining} end
local state = string.sub(current, 1, separator - 1)
local stored = tonumber(string.sub(current, separator + 1))
if stored ~= quantity then return {-2, 0, used, remaining} end
if state == 'completed' then return {2, 1, used, remaining} end
if state ~= 'admitted' then return {-2, 0, used, remaining} end
redis.call('HSET', KEYS[1], field, 'completed|' .. quantity)
redis.call('PEXPIRE', KEYS[1], ttl)
return {2, 0, used, remaining}
`

const compensateScript = `
local field = ARGV[1]
local quantity = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local current = redis.call('HGET', KEYS[1], field)
local used = tonumber(redis.call('HGET', KEYS[1], 'used') or '0')
local capacity = tonumber(redis.call('HGET', KEYS[1], 'capacity') or used)
local remaining = capacity - used
if remaining < 0 then remaining = 0 end
if not current then return {-1, 0, used, remaining} end
local separator = string.find(current, '|', 1, true)
if not separator then return {-2, 0, used, remaining} end
local state = string.sub(current, 1, separator - 1)
local stored = tonumber(string.sub(current, separator + 1))
if stored ~= quantity then return {-2, 0, used, remaining} end
if state == 'compensated' then return {3, 1, used, remaining} end
if state ~= 'admitted' then return {-2, 0, used, remaining} end
used = used - quantity
if used < 0 then return {-2, 0, used, remaining} end
remaining = capacity - used
if remaining < 0 then remaining = 0 end
redis.call('HSET', KEYS[1], 'used', used, field, 'compensated|' .. quantity)
redis.call('PEXPIRE', KEYS[1], ttl)
return {3, 0, used, remaining}
`
