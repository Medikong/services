package session

import "context"

// RevocationFence restores or finalizes the cache fence after the owning
// database transaction has rolled back or committed.
type RevocationFence interface {
	Resolve(context.Context) error
}
