package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Medikong/services/services/coupon-service/internal/model"
)

const pendingValue = "__pending__"

type Redis struct {
	client        redis.Cmdable
	keyPrefix     string
	pendingTTL    time.Duration
	idempotentTTL time.Duration
}

type RedisConfig struct {
	Client        redis.Cmdable
	KeyPrefix     string
	PendingTTL    time.Duration
	IdempotentTTL time.Duration
}

func NewRedis(config RedisConfig) *Redis {
	prefix := config.KeyPrefix
	if prefix == "" {
		prefix = "coupon"
	}
	pendingTTL := config.PendingTTL
	if pendingTTL <= 0 {
		pendingTTL = 30 * time.Second
	}
	idempotentTTL := config.IdempotentTTL
	if idempotentTTL <= 0 {
		idempotentTTL = 24 * time.Hour
	}
	return &Redis{
		client:        config.Client,
		keyPrefix:     prefix,
		pendingTTL:    pendingTTL,
		idempotentTTL: idempotentTTL,
	}
}

func NewRedisClient(rawURL string) (*redis.Client, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("redis url is required")
	}
	options, err := redis.ParseURL(rawURL)
	if err == nil {
		return redis.NewClient(options), nil
	}
	return redis.NewClient(&redis.Options{Addr: rawURL}), nil
}

func (r *Redis) PreparePolicy(ctx context.Context, policy model.Policy) error {
	remaining := policy.TotalQuantity - policy.IssuedCount
	if remaining < 0 {
		remaining = 0
	}
	return r.client.Set(ctx, r.remainingKey(policy.PolicyID), remaining, 0).Err()
}

func (r *Redis) Admit(ctx context.Context, request IssueRequest) (Decision, error) {
	values, err := admitScript.Run(ctx, r.client, []string{
		r.remainingKey(request.PolicyID),
		r.issuedKey(request.PolicyID, request.UserID),
		r.idempotencyKey(request.PolicyID, request.UserID, request.IdempotencyKey),
	}, request.IdempotencyKey, strconv.Itoa(int(r.pendingTTL.Seconds()))).Slice()
	if err != nil {
		return Decision{}, err
	}
	if len(values) != 2 {
		return Decision{}, fmt.Errorf("redis gate admit returned %d values", len(values))
	}
	result, ok := values[0].(string)
	if !ok {
		return Decision{}, fmt.Errorf("redis gate admit result has type %T", values[0])
	}
	decision := Decision{
		Result:         result,
		PolicyID:       request.PolicyID,
		UserID:         request.UserID,
		IdempotencyKey: request.IdempotencyKey,
	}
	rawCoupon, ok := values[1].(string)
	if ok && rawCoupon != "" && rawCoupon != pendingValue {
		if err := json.Unmarshal([]byte(rawCoupon), &decision.Coupon); err != nil {
			return Decision{}, fmt.Errorf("decode redis gate coupon: %w", err)
		}
	}
	return decision, nil
}

func (r *Redis) Complete(ctx context.Context, decision Decision, result model.IssueResult) error {
	if decision.Result != ResultIssuedCandidate {
		return nil
	}
	couponJSON, err := json.Marshal(result.Coupon)
	if err != nil {
		return err
	}
	restoreRemaining := result.Result == ResultDuplicate
	return completeScript.Run(ctx, r.client, []string{
		r.remainingKey(decision.PolicyID),
		r.issuedKey(decision.PolicyID, decision.UserID),
		r.idempotencyKey(decision.PolicyID, decision.UserID, decision.IdempotencyKey),
	}, string(couponJSON), decision.IdempotencyKey, strconv.Itoa(int(r.idempotentTTL.Seconds())), strconv.FormatBool(restoreRemaining)).Err()
}

func (r *Redis) Compensate(ctx context.Context, decision Decision) error {
	if decision.Result != ResultIssuedCandidate {
		return nil
	}
	return compensateScript.Run(ctx, r.client, []string{
		r.remainingKey(decision.PolicyID),
		r.issuedKey(decision.PolicyID, decision.UserID),
		r.idempotencyKey(decision.PolicyID, decision.UserID, decision.IdempotencyKey),
	}, decision.IdempotencyKey).Err()
}

func (r *Redis) remainingKey(policyID string) string {
	return fmt.Sprintf("%s:%s:remaining", r.keyPrefix, policyID)
}

func (r *Redis) issuedKey(policyID string, userID string) string {
	return fmt.Sprintf("%s:%s:issued:%s", r.keyPrefix, policyID, userID)
}

func (r *Redis) idempotencyKey(policyID string, userID string, key string) string {
	if key == "" {
		return fmt.Sprintf("%s:%s:idem:%s:-", r.keyPrefix, policyID, userID)
	}
	return fmt.Sprintf("%s:%s:idem:%s:%s", r.keyPrefix, policyID, userID, key)
}

var admitScript = redis.NewScript(`
local issued = redis.call("GET", KEYS[2])
if issued then
  return {"duplicate", issued}
end

if ARGV[1] ~= "" then
  local idem = redis.call("GET", KEYS[3])
  if idem then
    return {"duplicate", idem}
  end
end

local remaining = redis.call("GET", KEYS[1])
if not remaining then
  return {"not_ready", ""}
end

if tonumber(remaining) <= 0 then
  return {"sold_out", ""}
end

redis.call("DECR", KEYS[1])
redis.call("SET", KEYS[2], "__pending__", "EX", tonumber(ARGV[2]))
if ARGV[1] ~= "" then
  redis.call("SET", KEYS[3], "__pending__", "EX", tonumber(ARGV[2]))
end
return {"issued_candidate", ""}
`)

var completeScript = redis.NewScript(`
if ARGV[4] == "true" then
  redis.call("INCR", KEYS[1])
end
redis.call("SET", KEYS[2], ARGV[1])
if ARGV[2] ~= "" then
  redis.call("SET", KEYS[3], ARGV[1], "EX", tonumber(ARGV[3]))
end
return {"ok"}
`)

var compensateScript = redis.NewScript(`
local issued = redis.call("GET", KEYS[2])
if issued == "__pending__" then
  redis.call("DEL", KEYS[2])
  redis.call("INCR", KEYS[1])
end
if ARGV[1] ~= "" then
  local idem = redis.call("GET", KEYS[3])
  if idem == "__pending__" then
    redis.call("DEL", KEYS[3])
  end
end
return {"ok"}
`)
