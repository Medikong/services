package rolegrant

import "context"

type Repository interface {
	Grant(ctx context.Context, grant Grant) error
	Replace(ctx context.Context, authAccountID string, roles []string) error
	ListByAuthAccountID(ctx context.Context, authAccountID string) ([]string, error)
}
