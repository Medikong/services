package operator

import (
	"context"
	"errors"
)

// ApprovalPort isolates the external case/approval system. No production URL
// or credential is invented here; the default adapter rejects until a trusted
// integration is explicitly configured.
type ApprovalPort interface {
	Verify(context.Context, ApprovalRequest) error
}
type ApprovalRequest struct {
	CaseID, ApprovalID, EvidenceRef, Action, TargetType, TargetID string
}
type DenyApprovalPort struct{}

func (DenyApprovalPort) Verify(context.Context, ApprovalRequest) error {
	return errors.New("approval integration is not configured")
}

type StaticApprovalPort struct{ Allow bool }

func (p StaticApprovalPort) Verify(context.Context, ApprovalRequest) error {
	if p.Allow {
		return nil
	}
	return errors.New("approval denied")
}
