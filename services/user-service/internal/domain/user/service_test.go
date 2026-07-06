package user

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
)

func TestEnsureAndMeAreIdempotent(t *testing.T) {
	svc := NewService(NewMemoryRepository())
	ctx := context.Background()
	first, err := svc.Ensure(ctx, EnsureInput{UserID: "user-1"})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	second, err := svc.Me(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-1"})
	if err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if first.UserID != second.UserID || first.RealName != second.RealName {
		t.Fatalf("users differ: first=%+v second=%+v", first, second)
	}
}

func TestUpdateMyProfile(t *testing.T) {
	svc := NewService(NewMemoryRepository())
	ctx := context.Background()
	nickname := "dropper"
	icon := "https://cdn.example.test/icon.png"
	user, err := svc.UpdateMyProfile(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, ProfileUpdate{
		Nickname:    &nickname,
		ProfileIcon: &icon,
	})
	if err != nil {
		t.Fatalf("UpdateMyProfile() error = %v", err)
	}
	if user.Nickname != nickname || user.ProfileIcon != icon || user.Status != "active" {
		t.Fatalf("user = %+v", user)
	}
}

func TestUpdateMyProfileRejectsMissingUserID(t *testing.T) {
	svc := NewService(NewMemoryRepository())
	if _, err := svc.UpdateMyProfile(context.Background(), principal.Principal{Type: principal.TypeUser}, ProfileUpdate{}); err != ErrUnauthorized {
		t.Fatalf("err = %v, want %v", err, ErrUnauthorized)
	}
}
