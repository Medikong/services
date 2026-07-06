package account

import "testing"

func TestNewDefaultsToActiveAccount(t *testing.T) {
	account, err := New(" auth_123 ")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if account.AuthAccountID != "auth_123" {
		t.Fatalf("AuthAccountID = %q", account.AuthAccountID)
	}
	if account.Status != StatusActive {
		t.Fatalf("Status = %q", account.Status)
	}
}

func TestAccountValidateRejectsInvalidState(t *testing.T) {
	account := Account{AuthAccountID: "auth_123", Status: Status("deleted")}
	if err := account.Validate(); err == nil {
		t.Fatal("Validate() succeeded with invalid status")
	}
}

func TestValidPasswordRequiresMinimumStrength(t *testing.T) {
	for _, password := range []string{"", "        ", "short1", "password", "12345678", "secret 123", " secret123"} {
		if validPassword(password) {
			t.Fatalf("validPassword(%q) = true, want false", password)
		}
	}
	if !validPassword("secret-123") {
		t.Fatal("validPassword(secret-123) = false, want true")
	}
}
