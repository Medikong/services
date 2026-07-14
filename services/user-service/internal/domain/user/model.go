package user

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type AccountStatus string

const (
	StatusActive      AccountStatus = "active"
	StatusRestricted  AccountStatus = "restricted"
	StatusDeactivated AccountStatus = "deactivated"
)

var (
	ErrNotFound             = errors.New("user not found")
	ErrAccountNotActive     = errors.New("user account is not active")
	ErrVersionConflict      = errors.New("user version conflict")
	ErrIdempotencyConflict  = errors.New("user idempotency conflict")
	ErrRegistrationConflict = errors.New("user registration conflict")
	ErrTransitionInvalid    = errors.New("user status transition invalid")
)

type User struct {
	ID                  uuid.UUID
	RegistrationID      string
	AccountStatus       AccountStatus
	Nickname            string
	Introduction        *string
	ProfileMediaAssetID *string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type AgreementAcceptance struct {
	Code       string
	Version    string
	AcceptedAt time.Time
}

type ProfilePatch struct {
	NicknameSet     bool
	Nickname        string
	IntroductionSet bool
	Introduction    *string
}

func (p ProfilePatch) ChangedFields() []string {
	fields := make([]string, 0, 2)
	if p.NicknameSet {
		fields = append(fields, "nickname")
	}
	if p.IntroductionSet {
		fields = append(fields, "introduction")
	}
	return fields
}

func NormalizeRegistrationID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 128 {
		return "", fmt.Errorf("registrationId must contain 1 to 128 bytes")
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return "", fmt.Errorf("registrationId contains control or whitespace characters")
		}
	}
	return value, nil
}

func NormalizePrivateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if count := utf8.RuneCountInString(value); count < 1 || count > 100 {
		return "", fmt.Errorf("privateName must contain 1 to 100 characters")
	}
	return value, nil
}

func NormalizeNickname(value string) (string, error) {
	value = strings.TrimSpace(value)
	if count := utf8.RuneCountInString(value); count < 1 || count > 50 {
		return "", fmt.Errorf("nickname must contain 1 to 50 characters")
	}
	return value, nil
}

func NormalizeIntroduction(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if utf8.RuneCountInString(normalized) > 500 {
		return nil, fmt.Errorf("introduction must contain at most 500 characters")
	}
	if normalized == "" {
		return nil, nil
	}
	return &normalized, nil
}

func NormalizeMediaAssetID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 128 {
		return "", fmt.Errorf("mediaAssetId must contain 1 to 128 bytes")
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return "", fmt.Errorf("mediaAssetId contains control or whitespace characters")
		}
	}
	return value, nil
}

func ParseStatus(value string) (AccountStatus, error) {
	status := AccountStatus(strings.TrimSpace(value))
	switch status {
	case StatusActive, StatusRestricted, StatusDeactivated:
		return status, nil
	default:
		return "", fmt.Errorf("unsupported account status %q", value)
	}
}

func AllowedPreviousStatuses(target AccountStatus) []string {
	switch target {
	case StatusActive:
		return []string{string(StatusRestricted), string(StatusDeactivated)}
	case StatusRestricted:
		return []string{string(StatusActive)}
	case StatusDeactivated:
		return []string{string(StatusActive), string(StatusRestricted)}
	default:
		return nil
	}
}

func CanTransition(from, to AccountStatus) bool {
	for _, allowed := range AllowedPreviousStatuses(to) {
		if string(from) == allowed {
			return true
		}
	}
	return false
}
