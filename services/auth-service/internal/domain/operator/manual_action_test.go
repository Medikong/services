package operator

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestManualActionValidateKeepsActionAndTargetPaired(t *testing.T) {
	valid := ManualAction{
		ID: uuid.New(), OperatorID: uuid.New(), CaseID: "case-1",
		TargetType: "identity_link", TargetID: uuid.NewString(), Action: "revoke_identity_link",
		ReasonCode: "CUSTOMER_SUPPORT", ApprovalID: "approval-1", EvidenceRef: "evidence-1",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("validate manual action: %v", err)
	}
	invalid := valid
	invalid.TargetType = "session"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidManualAction) {
		t.Fatalf("invalid action-target error = %v", err)
	}
}
