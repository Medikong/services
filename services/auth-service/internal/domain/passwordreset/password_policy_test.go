package passwordreset

import (
	"context"
	"strings"
	"testing"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

func TestCompleteUsesConfiguredPasswordPolicy(t *testing.T) {
	service := &Service{config: Config{PasswordPolicy: security.PasswordPolicy{
		MinimumLength: 16,
		MaximumLength: security.DefaultPasswordMaximumLength,
		MaximumBytes:  security.DefaultPasswordMaximumBytes,
	}}}
	password := strings.Repeat("a", 15)
	err := service.Complete(context.Background(), CompleteInput{
		ResetID:         uuid.NewString(),
		NewPassword:     password,
		ConfirmPassword: password,
		IdempotencyKey:  uuid.NewString(),
	})
	if err == nil {
		t.Fatal("configured password minimum was not enforced")
	}
	oopsErr, ok := oops.AsOops(err)
	if !ok || oopsErr.Code() != "AUTH_PASSWORD_POLICY_NOT_MET" || !strings.Contains(err.Error(), "16자 이상") {
		t.Fatalf("password policy error = %v", err)
	}
}
