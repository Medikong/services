package coupon

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/redis/go-redis/v9"
)

func TestIssueDuplicateAndSoldOut(t *testing.T) {
	svc := NewService(NewMemoryRepository())
	ctx := context.Background()
	if _, err := svc.PreparePolicy(ctx, PreparePolicyInput{PolicyID: "policy-1", DropID: "drop-1", Name: "Launch", TotalQuantity: 1}); err != nil {
		t.Fatalf("PreparePolicy() error = %v", err)
	}
	user := principal.Principal{Type: principal.TypeUser, UserID: "user-1"}
	first, err := svc.Issue(ctx, user, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if err != nil {
		t.Fatalf("Issue() first error = %v", err)
	}
	if first.Result != "issued" {
		t.Fatalf("first result = %q", first.Result)
	}
	duplicate, err := svc.Issue(ctx, user, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if err != nil {
		t.Fatalf("Issue() duplicate error = %v", err)
	}
	if duplicate.Result != "duplicate" {
		t.Fatalf("duplicate result = %q", duplicate.Result)
	}
	_, err = svc.Issue(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-2"}, IssueInput{PolicyID: "policy-1"}, "idem-2")
	if err == nil {
		t.Fatalf("Issue() sold out error = nil")
	}
}

func TestIssueFallsBackToStoreWhenRedisUnavailable(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  time.Millisecond,
		ReadTimeout:  time.Millisecond,
		WriteTimeout: time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = redisClient.Close() })

	svc := NewService(NewMemoryRepository(), WithRedis(redisClient, time.Second, time.Hour))
	ctx := context.Background()
	if _, err := svc.PreparePolicy(ctx, PreparePolicyInput{PolicyID: "policy-redis-down", DropID: "drop-1", Name: "Launch", TotalQuantity: 1}); err != nil {
		t.Fatalf("PreparePolicy() error = %v", err)
	}
	result, err := svc.Issue(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, IssueInput{PolicyID: "policy-redis-down"}, "idem-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if result.Result != "issued" {
		t.Fatalf("result = %q, want issued", result.Result)
	}
}
