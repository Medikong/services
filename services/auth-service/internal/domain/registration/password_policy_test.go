package registration

import (
	"context"
	"strings"
	"testing"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/samber/oops"
)

func TestStartUsesConfiguredPasswordPolicy(t *testing.T) {
	service := &Service{config: Config{PasswordPolicy: security.PasswordPolicy{
		MinimumLength: 16,
		MaximumLength: security.DefaultPasswordMaximumLength,
		MaximumBytes:  security.DefaultPasswordMaximumBytes,
	}}}
	_, err := service.Start(context.Background(), StartInput{
		IdempotencyKey:     "registration-policy-test",
		ProfileRequestID:   "profile-policy-test",
		AgreementReceiptID: "agreement-policy-test",
		Email:              "policy@example.test",
		Phone:              "+821012345678",
		Password:           strings.Repeat("a", 15),
	})
	if err == nil {
		t.Fatal("configured password minimum was not enforced")
	}
	oopsErr, ok := oops.AsOops(err)
	if !ok || oopsErr.Code() != "AUTH_PASSWORD_POLICY_NOT_MET" || !strings.Contains(err.Error(), "16자 이상") {
		t.Fatalf("password policy error = %v", err)
	}
}
