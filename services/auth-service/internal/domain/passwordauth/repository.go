package passwordauth

import "context"

type Repository interface {
	CreatePassword(ctx context.Context, credential PasswordCredential) error
	FindPasswordByEmail(ctx context.Context, email string) (PasswordCredential, error)
}
