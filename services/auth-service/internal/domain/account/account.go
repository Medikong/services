package account

import (
	"errors"
	"strings"
	"time"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

type Account struct {
	AuthAccountID string
	Status        Status
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func New(authAccountID string) (Account, error) {
	account := Account{
		AuthAccountID: strings.TrimSpace(authAccountID),
		Status:        StatusActive,
	}
	if err := account.Validate(); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (a Account) Validate() error {
	if strings.TrimSpace(a.AuthAccountID) == "" {
		return errors.New("auth account id is required")
	}
	switch a.Status {
	case "", StatusActive:
		return nil
	case StatusDisabled:
		return nil
	default:
		return errors.New("auth account status is invalid")
	}
}

func (a Account) statusOrDefault() Status {
	if a.Status == "" {
		return StatusActive
	}
	return a.Status
}
