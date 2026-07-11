package operator

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAuthorizeRequiresCurrentGrantPermissionAndStrongSession(t *testing.T) {
	userID := uuid.New()
	service := &Service{
		access:    authorizationAccess{state: access.State{UserID: userID, Status: "active"}, grant: access.Grant{UserID: userID, Status: "active", Version: 3, Roles: []string{"platform_operator"}, Permissions: []string{"auth.policy.read"}}},
		strongTTL: 5 * time.Minute,
	}
	principal := appsession.Principal{Authenticated: true, UserID: userID, Channel: "web", Method: "email_password", AuthenticatedAt: time.Now().UTC(), GrantVersion: 3}
	if err := service.authorize(context.Background(), principal, false, "auth.policy.read"); err != nil {
		t.Fatalf("authorize current permitted strong principal: %v", err)
	}
	if err := service.authorize(context.Background(), principal, false, "auth.policy.write"); application.AsError(err).Code != "AUTH_FORBIDDEN" {
		t.Fatalf("missing permission error=%v", err)
	}
	principal.AuthenticatedAt = time.Now().UTC().Add(-6 * time.Minute)
	if err := service.authorize(context.Background(), principal, true, "auth.policy.read"); application.AsError(err).Code != "AUTH_REAUTH_REQUIRED" {
		t.Fatalf("stale strong-auth error=%v", err)
	}
	principal.AuthenticatedAt = time.Now().UTC()
	principal.GrantVersion = 2
	if err := service.authorize(context.Background(), principal, false, "auth.policy.read"); application.AsError(err).Code != "AUTH_FORBIDDEN" {
		t.Fatalf("stale grant error=%v", err)
	}
}

type authorizationAccess struct {
	state access.State
	grant access.Grant
	err   error
}

func (r authorizationAccess) CreateActiveForRegistration(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (r authorizationAccess) FindActiveForUpdate(context.Context, pgx.Tx, uuid.UUID) (access.State, access.Grant, error) {
	return r.state, r.grant, r.err
}
func (r authorizationAccess) FindActive(context.Context, uuid.UUID) (access.State, access.Grant, error) {
	return r.state, r.grant, r.err
}
func (r authorizationAccess) Restrict(context.Context, pgx.Tx, uuid.UUID, string, int64) error {
	return nil
}
