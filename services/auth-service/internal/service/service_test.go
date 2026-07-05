package service

import (
	"context"
	"testing"

	"github.com/Medikong/services/services/auth-service/internal/store/memory"
)

func TestSignupLoginAndIntrospect(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()
	signup, err := svc.Signup(ctx, SignupInput{Email: "A@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	login, err := svc.Login(ctx, LoginInput{Email: "a@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.UserID != signup.UserID {
		t.Fatalf("login userID = %q, want %q", login.UserID, signup.UserID)
	}
	p, err := svc.Introspect(ctx, "Bearer "+login.AccessToken)
	if err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if p.UserID != signup.UserID || !p.HasRole("customer") {
		t.Fatalf("principal = %+v", p)
	}
	if p.SessionID == "" || p.AuthLevel != "normal" || p.ClientType != "api" || len(p.AuthMethods) != 1 || p.AuthMethods[0] != "password" {
		t.Fatalf("principal auth contract = %+v", p)
	}
}

func TestRefreshRotatesTokensAndInvalidatesOldAccessToken(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()
	signup, err := svc.Signup(ctx, SignupInput{Email: "rotate@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	refreshed, err := svc.Refresh(ctx, RefreshInput{RefreshToken: signup.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.AccessToken == signup.AccessToken || refreshed.RefreshToken == signup.RefreshToken {
		t.Fatalf("tokens were not rotated: signup=%+v refreshed=%+v", signup, refreshed)
	}
	if _, err := svc.Introspect(ctx, "Bearer "+signup.AccessToken); err == nil {
		t.Fatal("old access token introspected after refresh")
	}
	if _, err := svc.Refresh(ctx, RefreshInput{RefreshToken: signup.RefreshToken}); err == nil {
		t.Fatal("old refresh token succeeded after rotation")
	}
}

func TestLogoutRejectsFutureIntrospect(t *testing.T) {
	svc := New(memory.New())
	ctx := context.Background()
	login, err := svc.Signup(ctx, SignupInput{Email: "logout@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	if err := svc.Logout(ctx, "Bearer "+login.AccessToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := svc.Introspect(ctx, "Bearer "+login.AccessToken); err == nil {
		t.Fatal("introspect succeeded after logout")
	}
}

func TestAuthzCacheInvalidatesOnLogout(t *testing.T) {
	store := memory.New()
	cache := NewMemoryAuthzCache()
	svc := New(store, WithAuthzCache(cache))
	ctx := context.Background()
	login, err := svc.Signup(ctx, SignupInput{Email: "cache@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	if _, err := svc.Introspect(ctx, "Bearer "+login.AccessToken); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if err := svc.Logout(ctx, "Bearer "+login.AccessToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, ok := cache.Get(login.AccessToken); ok {
		t.Fatal("cache retained revoked token")
	}
}

func TestAuthzCacheInvalidatesOnRevoke(t *testing.T) {
	store := memory.New()
	cache := NewMemoryAuthzCache()
	svc := New(store, WithAuthzCache(cache))
	ctx := context.Background()
	login, err := svc.Signup(ctx, SignupInput{Email: "revoke-cache@example.com", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	if _, err := svc.Introspect(ctx, "Bearer "+login.AccessToken); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if err := svc.Revoke(ctx, login.Principal.SessionID); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, ok := cache.Get(login.AccessToken); ok {
		t.Fatal("cache retained revoked token")
	}
}
