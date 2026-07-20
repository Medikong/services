package identity

import "testing"

func TestPasswordPolicyValidate(t *testing.T) {
	t.Parallel()

	policy := PasswordPolicy{MinimumLength: 12}
	if err := policy.Validate("short"); err == nil {
		t.Fatal("short password must be rejected")
	}
	if err := policy.Validate("long-enough-password"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}
