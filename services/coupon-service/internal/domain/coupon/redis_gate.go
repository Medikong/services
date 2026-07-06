package coupon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const pendingValue = "__pending__"

const (
	resultIssuedCandidate = "issued_candidate"
	resultDuplicate       = "duplicate"
	resultSoldOut         = "sold_out"
	resultNotReady        = "not_ready"
	resultPending         = "pending"
)

type redisDecision struct {
	Result         string
	PolicyID       string
	UserID         string
	IdempotencyKey string
	Coupon         Coupon
}

func redisRemainingKey(prefix string, policyID string) string {
	return fmt.Sprintf("%s:%s:remaining", prefix, policyID)
}

func redisIssuedKey(prefix string, policyID string, userID string) string {
	return fmt.Sprintf("%s:%s:issued:%s", prefix, policyID, userID)
}

func redisIdempotencyKey(prefix string, policyID string, userID string, key string) string {
	if key == "" {
		return fmt.Sprintf("%s:%s:idem:%s:-", prefix, policyID, userID)
	}
	return fmt.Sprintf("%s:%s:idem:%s:%s", prefix, policyID, userID, key)
}

func runRedisAdmitScript(ctx context.Context, client redis.Cmdable, prefix string, policyID string, userID string, idempotencyKey string, pendingTTL time.Duration) (redisDecision, error) {
	values, err := admitScript.Run(ctx, client, []string{
		redisRemainingKey(prefix, policyID),
		redisIssuedKey(prefix, policyID, userID),
		redisIdempotencyKey(prefix, policyID, userID, idempotencyKey),
	}, idempotencyKey, strconv.Itoa(int(pendingTTL.Seconds()))).Slice()
	if err != nil {
		return redisDecision{}, err
	}
	if len(values) != 2 {
		return redisDecision{}, fmt.Errorf("redis gate admit returned %d values", len(values))
	}
	result, ok := values[0].(string)
	if !ok {
		return redisDecision{}, fmt.Errorf("redis gate admit result has type %T", values[0])
	}
	decision := redisDecision{
		Result:         result,
		PolicyID:       policyID,
		UserID:         userID,
		IdempotencyKey: idempotencyKey,
	}
	rawCoupon, ok := values[1].(string)
	if ok && rawCoupon == pendingValue {
		decision.Result = resultPending
	}
	if ok && rawCoupon != "" && rawCoupon != pendingValue {
		if err := json.Unmarshal([]byte(rawCoupon), &decision.Coupon); err != nil {
			return redisDecision{}, fmt.Errorf("decode redis gate coupon: %w", err)
		}
	}
	return decision, nil
}

func runRedisCompleteScript(ctx context.Context, client redis.Cmdable, prefix string, decision redisDecision, result IssueResult, idempotentTTL time.Duration) error {
	couponJSON, err := json.Marshal(result.Coupon)
	if err != nil {
		return err
	}
	restoreRemaining := result.Result == "duplicate"
	return completeScript.Run(ctx, client, []string{
		redisRemainingKey(prefix, decision.PolicyID),
		redisIssuedKey(prefix, decision.PolicyID, decision.UserID),
		redisIdempotencyKey(prefix, decision.PolicyID, decision.UserID, decision.IdempotencyKey),
	}, string(couponJSON), decision.IdempotencyKey, strconv.Itoa(int(idempotentTTL.Seconds())), strconv.FormatBool(restoreRemaining)).Err()
}

func runRedisCompensateScript(ctx context.Context, client redis.Cmdable, prefix string, decision redisDecision) error {
	return compensateScript.Run(ctx, client, []string{
		redisRemainingKey(prefix, decision.PolicyID),
		redisIssuedKey(prefix, decision.PolicyID, decision.UserID),
		redisIdempotencyKey(prefix, decision.PolicyID, decision.UserID, decision.IdempotencyKey),
	}, decision.IdempotencyKey).Err()
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
