package providerlink

import "context"

type Repository interface {
	Create(ctx context.Context, link Link) (Link, error)
	FindByProviderSubject(ctx context.Context, provider string, subject string) (Link, error)
}
