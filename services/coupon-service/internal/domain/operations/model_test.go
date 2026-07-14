package operations

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyStopAllowsOnlyDesignedScopes(t *testing.T) {
	now := time.Now().UTC()
	control, domainEvent, err := ApplyStop(Stop{
		ControlID: "ctrl_12345678", Scopes: []Scope{{Type: ScopeCampaign, Ref: "camp_12345678"}},
		Active: true, EffectiveFrom: now, BlockIssuance: true,
		OperationRequestRef: "task-1", ApprovalRef: "approval-1", ReasonCode: "incident", AppliedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, "EVT.A.19-25", domainEvent.DocumentID)
	require.True(t, control.BlockIssuance)

	_, _, err = ApplyStop(Stop{
		ControlID: "ctrl_87654321", Scopes: []Scope{{Type: "store", Ref: "store-1"}},
		Active: true, EffectiveFrom: now, BlockIssuance: true,
		OperationRequestRef: "task-2", ApprovalRef: "approval-2", AppliedAt: now,
	})
	require.Error(t, err)
}

func TestApplyNoticeDoesNotChangeStopLifecycle(t *testing.T) {
	now := time.Now().UTC()
	control := validControl(now)
	stopEffective := control.EffectiveFrom
	domainEvent, err := control.ApplyNotice(NoticeUpdate{
		ExpectedVersion: 0, Message: "쿠폰 사용이 잠시 제한됩니다.",
		EffectiveFrom: now.Add(time.Minute), Active: true, AppliedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, "EVT.A.19-38", domainEvent.DocumentID)
	require.Equal(t, stopEffective, control.EffectiveFrom)
	require.True(t, control.Active)
	require.True(t, control.BlockRedemption)
	require.True(t, control.Notice.Active)
}

func TestApplyNoticeValidatesVersionAndLength(t *testing.T) {
	now := time.Now().UTC()
	control := validControl(now)
	_, err := control.ApplyNotice(NoticeUpdate{
		ExpectedVersion: 1, Message: "안내", EffectiveFrom: now, Active: true, AppliedAt: now,
	})
	require.Error(t, err)
	_, err = control.ApplyNotice(NoticeUpdate{
		ExpectedVersion: 0, Message: strings.Repeat("가", 501), EffectiveFrom: now, Active: true, AppliedAt: now,
	})
	require.Error(t, err)
}

func validControl(now time.Time) Control {
	return Control{
		ID: "ctrl_12345678", Scopes: []Scope{{Type: ScopeDrop, Ref: "drop-1"}}, Active: true,
		EffectiveFrom: now.Add(-time.Minute), BlockRedemption: true,
		OperationRequestRef: "task-1", ApprovalRef: "approval-1", CreatedAt: now, UpdatedAt: now,
	}
}
