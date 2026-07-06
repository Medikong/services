package account

import "context"

type Repository interface {
	Create(ctx context.Context, account Account) (Account, error)
	Ensure(ctx context.Context, account Account) (Account, error)
	FindByID(ctx context.Context, authAccountID string) (Account, error)
}
