package operator

import (
	"context"

	"github.com/samber/oops"
)

type ApprovalPort interface {
	Verify(context.Context, ApprovalRequest) error
}

type ApprovalRequest struct {
	CaseID, ApprovalID, EvidenceRef, Action, TargetType, TargetID string
}

type DenyApprovalPort struct{}

func (DenyApprovalPort) Verify(context.Context, ApprovalRequest) error {
	return oops.In("operator_approval").Code("approval.not_configured").New("approval integration is not configured")
}

type StaticApprovalPort struct{ Allow bool }

func (p StaticApprovalPort) Verify(context.Context, ApprovalRequest) error {
	if p.Allow {
		return nil
	}
	return oops.In("operator_approval").Code("approval.denied").New("approval denied")
}

type DenyAuthorizationDecisionPort struct{}

func (DenyAuthorizationDecisionPort) Verify(context.Context, string, string, string, string) error {
	return oops.In("operator_authorization").Code("authorization.not_configured").New("authorization decision verifier is not configured")
}
