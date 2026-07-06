package userlink

import "context"

type Link struct {
	AuthAccountID string
	UserID        string
}

type Repository interface {
	Create(ctx context.Context, link Link) error
	Upsert(ctx context.Context, link Link) error
	FindByAuthAccountID(ctx context.Context, authAccountID string) (Link, error)
}
