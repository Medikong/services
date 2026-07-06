package account

import "context"

type Account struct {
	AuthAccountID string
}

type Repository interface {
	Create(ctx context.Context, account Account) error
}
