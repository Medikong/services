package userauthstate

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive      Status = "active"
	StatusRestricted  Status = "restricted"
	StatusDeactivated Status = "deactivated"
)

var ErrVersionConflict = errors.New("user auth state version conflict")

type State struct {
	UserID         uuid.UUID
	Status         Status
	UserVersion    int64
	StatusChangeID string
	EffectiveAt    time.Time
	RowVersion     int64
}

type Change struct {
	Status         Status
	UserVersion    int64
	StatusChangeID string
	ChangedAt      time.Time
}

func ParseStatus(value string) (Status, error) {
	status := Status(strings.TrimSpace(value))
	switch status {
	case StatusActive, StatusRestricted, StatusDeactivated:
		return status, nil
	default:
		return "", errors.New("unsupported account status")
	}
}

func (s State) Compare(change Change) (apply, replay bool, err error) {
	if change.UserVersion < 1 || strings.TrimSpace(change.StatusChangeID) == "" || change.ChangedAt.IsZero() {
		return false, false, errors.New("invalid user auth state change")
	}
	if change.UserVersion < s.UserVersion {
		return false, false, nil
	}
	if change.UserVersion == s.UserVersion {
		if change.StatusChangeID == s.StatusChangeID && change.Status == s.Status {
			return false, true, nil
		}
		return false, false, ErrVersionConflict
	}
	return true, false, nil
}
