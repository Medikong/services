package service

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/user-service/internal/model"
	"github.com/Medikong/services/services/user-service/internal/store/memory"
)

func TestEnsureAndMeAreIdempotent(t *testing.T) {
	svc := New(memory.New())
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
	svc := New(memory.New())
	ctx := context.Background()
	nickname := "dropper"
	icon := "https://cdn.example.test/icon.png"
	user, err := svc.UpdateMyProfile(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, model.ProfileUpdate{
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
	svc := New(memory.New())
	if _, err := svc.UpdateMyProfile(context.Background(), principal.Principal{Type: principal.TypeUser}, model.ProfileUpdate{}); err != ErrUnauthorized {
		t.Fatalf("err = %v, want %v", err, ErrUnauthorized)
	}
}
