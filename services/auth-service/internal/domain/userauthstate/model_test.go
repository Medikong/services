package userauthstate

import (
	"errors"
	"testing"
	"time"
)

func TestStateCompareEnforcesMonotonicVersionAndIdempotency(t *testing.T) {
	current := State{Status: StatusRestricted, UserVersion: 4, StatusChangeID: "change-4"}
	changedAt := time.Now().UTC()

	apply, replay, err := current.Compare(Change{Status: StatusActive, UserVersion: 3, StatusChangeID: "late", ChangedAt: changedAt})
	if err != nil || apply || replay {
		t.Fatalf("older change = apply %v replay %v err %v", apply, replay, err)
	}
	apply, replay, err = current.Compare(Change{Status: StatusRestricted, UserVersion: 4, StatusChangeID: "change-4", ChangedAt: changedAt})
	if err != nil || apply || !replay {
		t.Fatalf("same change = apply %v replay %v err %v", apply, replay, err)
	}
	_, _, err = current.Compare(Change{Status: StatusDeactivated, UserVersion: 4, StatusChangeID: "other", ChangedAt: changedAt})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("same version conflict error = %v", err)
	}
	apply, replay, err = current.Compare(Change{Status: StatusDeactivated, UserVersion: 5, StatusChangeID: "change-5", ChangedAt: changedAt})
	if err != nil || !apply || replay {
		t.Fatalf("newer change = apply %v replay %v err %v", apply, replay, err)
	}
}
