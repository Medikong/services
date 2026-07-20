package userauthstate

import (
	"context"
	"errors"
)

type DenyAuthorizationDecisionPort struct{}

func (DenyAuthorizationDecisionPort) Verify(context.Context, string, string, string, string) error {
	return errors.New("authorization decision verifier is not configured")
}
