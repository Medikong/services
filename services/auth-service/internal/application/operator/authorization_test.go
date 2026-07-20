package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func TestAuthorizeRequiresExternalDecisionAndStrongSession(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewService(nil, nil, Config{StrongAuthTTL: 5 * time.Minute}, nil, authorizationDecision{allow: true}, fixedClock{now: now})
	principal := domainsession.Principal{
		Authenticated: true, UserID: uuid.New(), SessionID: uuid.New(), Method: "email_password",
		AuthenticatedAt: now,
	}
	if err := service.authorize(context.Background(), principal, "signed-decision", false, "auth.policy.read", "auth-policies"); err != nil {
		t.Fatalf("authorize current decision and strong principal: %v", err)
	}
	if err := service.authorize(context.Background(), principal, "", false, "auth.policy.read", "auth-policies"); failureCode(err) != "AUTH_FORBIDDEN" {
		t.Fatalf("missing decision error=%v", err)
	}
	service.decisions = authorizationDecision{}
	if err := service.authorize(context.Background(), principal, "denied", false, "auth.policy.read", "auth-policies"); failureCode(err) != "AUTH_FORBIDDEN" {
		t.Fatalf("denied decision error=%v", err)
	}
	service.decisions = authorizationDecision{allow: true}
	principal.AuthenticatedAt = now.Add(-6 * time.Minute)
	if err := service.authorize(context.Background(), principal, "signed-decision", true, "auth.policy.write", "auth-policies"); failureCode(err) != "AUTH_REAUTH_REQUIRED" {
		t.Fatalf("stale strong-auth error=%v", err)
	}
}

func TestUserPreservesUnavailableFailureContract(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewService(failingTransactor{}, nil, Config{}, nil, authorizationDecision{allow: true}, fixedClock{now: now})
	_, err := service.User(context.Background(), domainsession.Principal{
		Authenticated: true, UserID: uuid.New(), SessionID: uuid.New(), Method: "email_password", AuthenticatedAt: now,
	}, "allow", uuid.NewString(), "CUSTOMER_SUPPORT", "request-1")
	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Kind != failure.KindUnavailable || typed.Code != "AUTH_SERVICE_UNAVAILABLE" || typed.PublicMessage != unavailableMessage {
		t.Fatalf("unavailable failure = %#v", err)
	}
}

func failureCode(err error) string {
	var typed *failure.Error
	if !errors.As(err, &typed) {
		return ""
	}
	return typed.Code
}

type authorizationDecision struct{ allow bool }

func (d authorizationDecision) Verify(context.Context, string, string, string, string) error {
	if d.allow {
		return nil
	}
	return errors.New("denied")
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type failingTransactor struct{}

func (failingTransactor) WithinTransaction(context.Context, func(TxRepositories) error) error {
	return errors.New("transaction unavailable")
}
