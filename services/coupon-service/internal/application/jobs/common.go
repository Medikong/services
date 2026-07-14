package jobs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samber/oops"
)

type Policy struct {
	BatchSize      int
	PageSize       int
	Lease          time.Duration
	AttemptTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

func (p Policy) validate() error {
	if p.BatchSize < 1 || p.PageSize < 1 || p.Lease <= 0 || p.AttemptTimeout <= 0 ||
		p.Lease < 2*p.AttemptTimeout || p.MaxAttempts < 1 || p.BaseBackoff <= 0 || p.MaxBackoff < p.BaseBackoff {
		return oops.In("coupon_jobs").Code("coupon.worker_policy_invalid").New("worker batch, page, lease, timeout, and retry settings are invalid")
	}
	return nil
}

type Failure struct {
	Code      string
	Retryable bool
}

type FailureClassifier interface {
	Classify(error) Failure
}

type FailureClassifierFunc func(error) Failure

func (f FailureClassifierFunc) Classify(err error) Failure { return f(err) }

type DefaultFailureClassifier struct{}

func (DefaultFailureClassifier) Classify(err error) Failure {
	if err == nil {
		return Failure{}
	}
	if errors.Is(err, context.Canceled) {
		return Failure{Code: "COUPON_WORKER_CANCELLED"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Failure{Code: "COUPON_WORKER_TIMEOUT", Retryable: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return Failure{Code: "COUPON_WORKER_DEPENDENCY_UNAVAILABLE", Retryable: true}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && retryableSQLState(pgErr.Code) {
		return Failure{Code: "COUPON_WORKER_POSTGRES_RETRYABLE", Retryable: true}
	}
	if hasOopsCode(err, "coupon.external_dependency_unavailable") {
		return Failure{Code: "COUPON_WORKER_DEPENDENCY_UNAVAILABLE", Retryable: true}
	}
	if hasOopsCode(err, "coupon.version_conflict", "coupon.bulk_page_lease_lost") {
		return Failure{Code: "COUPON_WORKER_CONCURRENT_UPDATE", Retryable: true}
	}
	return Failure{Code: "COUPON_WORKER_BUSINESS_REJECTED"}
}

func hasOopsCode(err error, expected ...string) bool {
	for current := err; current != nil; {
		value, ok := oops.AsOops(current)
		if !ok {
			current = errors.Unwrap(current)
			continue
		}
		code := fmt.Sprint(value.Code())
		for _, candidate := range expected {
			if code == candidate {
				return true
			}
		}
		current = errors.Unwrap(value)
	}
	return false
}

func retryableSQLState(code string) bool {
	return strings.HasPrefix(code, "08") || strings.HasPrefix(code, "53") ||
		strings.HasPrefix(code, "57") || code == "40001" || code == "40P01"
}

func retryDelay(attempt int, base, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for i := 1; i < attempt && delay < maximum; i++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func attemptContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}
