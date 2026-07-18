package domain

import "context"

type RevocationFences interface {
	Resolve(context.Context) error
}

type RevocationRollback interface {
	Rollback(context.Context) error
}

func ResolveRevocationRollback(ctx context.Context, tx RevocationRollback, fences RevocationFences) {
	cleanupCtx := context.WithoutCancel(ctx)
	_ = tx.Rollback(cleanupCtx)
	if fences != nil {
		_ = fences.Resolve(cleanupCtx)
	}
}
