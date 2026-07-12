package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	Reserve(context.Context, pgx.Tx, Identity) error
	FindByValueForUpdate(context.Context, pgx.Tx, Type, string) (Identity, error)
	FindByIDForUpdate(context.Context, pgx.Tx, uuid.UUID) (Identity, error)
	MarkVerified(context.Context, pgx.Tx, uuid.UUID) error
	CreatePasswordCredential(context.Context, pgx.Tx, uuid.UUID, string) error
	ReplacePasswordCredential(context.Context, pgx.Tx, uuid.UUID, string) error
	FindEmailCredentialForUpdate(context.Context, pgx.Tx, string) (Identity, Link, PasswordCredential, error)
	FindActivePhoneLinkForUpdate(context.Context, pgx.Tx, string) (Identity, Link, error)
	CreateActiveLink(context.Context, pgx.Tx, Link) error
	CreateRequestedLink(context.Context, pgx.Tx, Link) error
	CreatePhoneReplacementRequested(context.Context, pgx.Tx, Link, uuid.UUID, uuid.UUID) error
	AttachProofChallenge(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
	ActivateLink(context.Context, pgx.Tx, uuid.UUID) error
	ReplacePhoneLink(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
	RevokeLinksForUser(context.Context, pgx.Tx, uuid.UUID, Type, string) error
	RequestedLinkForUpdate(context.Context, pgx.Tx, uuid.UUID) (Link, Identity, error)
	FindActiveLinkForIdentityUser(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (Link, error)
	FindActiveLinkForIdentity(context.Context, pgx.Tx, uuid.UUID) (Link, error)
	FindActiveLinkForUserType(context.Context, pgx.Tx, uuid.UUID, Type) (Link, Identity, error)
	FindActiveEmailCredentialForUser(context.Context, pgx.Tx, uuid.UUID) (Identity, PasswordCredential, error)
}

type Clock func() time.Time
