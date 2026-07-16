package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

func TestAuthorizeRequiresExternalDecisionAndStrongSession(t *testing.T) {
	userID := uuid.New()
	service := &Service{decisions: authorizationDecision{allow: true}, strongTTL: 5 * time.Minute}
	principal := appsession.Principal{
		Authenticated: true, UserID: userID, SessionID: uuid.New(), Method: "email_password",
		AuthenticatedAt: time.Now().UTC(),
	}
	if err := service.authorize(context.Background(), principal, "signed-decision", false, "auth.policy.read", "auth-policies"); err != nil {
		t.Fatalf("authorize current decision and strong principal: %v", err)
	}
	if err := service.authorize(context.Background(), principal, "", false, "auth.policy.read", "auth-policies"); errorCode(err) != "AUTH_FORBIDDEN" {
		t.Fatalf("missing decision error=%v", err)
	}
	service.decisions = authorizationDecision{}
	if err := service.authorize(context.Background(), principal, "denied", false, "auth.policy.read", "auth-policies"); errorCode(err) != "AUTH_FORBIDDEN" {
		t.Fatalf("denied decision error=%v", err)
	}
	service.decisions = authorizationDecision{allow: true}
	principal.AuthenticatedAt = time.Now().UTC().Add(-6 * time.Minute)
	if err := service.authorize(context.Background(), principal, "signed-decision", true, "auth.policy.write", "auth-policies"); errorCode(err) != "AUTH_REAUTH_REQUIRED" {
		t.Fatalf("stale strong-auth error=%v", err)
	}
}

func errorCode(err error) string {
	oopsErr, ok := oops.AsOops(err)
	if !ok {
		return ""
	}
	code, _ := oopsErr.Code().(string)
	return code
}

type authorizationDecision struct{ allow bool }

func (d authorizationDecision) Verify(context.Context, string, string, string, string) error {
	if d.allow {
		return nil
	}
	return errors.New("denied")
}
