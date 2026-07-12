package challenge

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusIssued   Status = "issued"
	StatusVerified Status = "verified"
	StatusFailed   Status = "failed"
	StatusExpired  Status = "expired"
	StatusRevoked  Status = "revoked"
)

type Purpose string

const (
	PurposeSignupEmail   Purpose = "signup_email"
	PurposeSignupPhone   Purpose = "signup_phone"
	PurposePhoneSignIn   Purpose = "phone_signin"
	PurposePasswordReset Purpose = "password_reset"
	PurposePhoneChange   Purpose = "phone_change"
	PurposeIdentityLink  Purpose = "identity_link"
)

type Channel string

const (
	ChannelEmailCode Channel = "email_code"
	ChannelSMSCode   Channel = "sms_code"
)

type Method string

const (
	MethodEmail Method = "email"
	MethodPhone Method = "phone"
)

type SubjectType string

const (
	SubjectRegistration  SubjectType = "registration"
	SubjectPasswordReset SubjectType = "password_reset"
	SubjectIdentityLink  SubjectType = "identity_link"
	SubjectPhoneSignIn   SubjectType = "phone_signin"
	SubjectPhoneChange   SubjectType = "phone_change"
)

var (
	ErrInvalidChallenge   = errors.New("invalid verification challenge")
	ErrChallengeExpired   = errors.New("verification challenge expired")
	ErrChallengeClosed    = errors.New("verification challenge is closed")
	ErrVerificationFailed = errors.New("verification code did not match")
	ErrVirtualUnavailable = errors.New("virtual verification message unavailable")
)

// Challenge owns proof-consumption state. Secret plaintext is never part of
// this model; callers compare a keyed digest while the row is locked.
type Challenge struct {
	ID                    uuid.UUID
	SubjectType           SubjectType
	SubjectID             uuid.UUID
	Purpose               Purpose
	Method                Method
	Channel               Channel
	Destination           string
	DestinationLookupHash []byte
	IdentityID            *uuid.UUID
	CodeHash              []byte
	VerifierKeyVersion    int16
	Status                Status
	AttemptCount          int16
	MaxAttempts           int16
	SendCount             int16
	MaxSends              int16
	NextSendAt            time.Time
	PolicyVersion         *int64
	ExpiresAt             time.Time
	ConsumedAt            *time.Time
	VerifiedAt            *time.Time
	ClosedAt              *time.Time
	Version               int64
	CreatedAt             time.Time
}

type NewInput struct {
	ID                    uuid.UUID
	SubjectType           SubjectType
	SubjectID             uuid.UUID
	Purpose               Purpose
	Method                Method
	Channel               Channel
	Destination           string
	DestinationLookupHash []byte
	IdentityID            *uuid.UUID
	CodeHash              []byte
	VerifierKeyVersion    int16
	MaxAttempts           int16
	MaxSends              int16
	NextSendAt            time.Time
	PolicyVersion         *int64
	ExpiresAt             time.Time
	CreatedAt             time.Time
}

func New(input NewInput) (Challenge, error) {
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	challenge := Challenge{
		ID:                    input.ID,
		SubjectType:           input.SubjectType,
		SubjectID:             input.SubjectID,
		Purpose:               input.Purpose,
		Method:                input.Method,
		Channel:               input.Channel,
		Destination:           input.Destination,
		DestinationLookupHash: copyBytes(input.DestinationLookupHash),
		IdentityID:            copyUUID(input.IdentityID),
		CodeHash:              copyBytes(input.CodeHash),
		VerifierKeyVersion:    input.VerifierKeyVersion,
		Status:                StatusIssued,
		MaxAttempts:           input.MaxAttempts,
		SendCount:             1,
		MaxSends:              input.MaxSends,
		NextSendAt:            input.NextSendAt.UTC(),
		PolicyVersion:         copyInt64(input.PolicyVersion),
		ExpiresAt:             input.ExpiresAt.UTC(),
		CreatedAt:             createdAt,
	}
	if err := challenge.Validate(); err != nil {
		return Challenge{}, err
	}
	return challenge, nil
}

type ConsumeResult struct {
	Verified        bool
	AlreadyVerified bool
	Changed         bool
	Failure         ConsumeFailure
}

type ConsumeFailure string

const (
	ConsumeFailureNone     ConsumeFailure = ""
	ConsumeFailureMismatch ConsumeFailure = "mismatch"
	ConsumeFailureExpired  ConsumeFailure = "expired"
	ConsumeFailureClosed   ConsumeFailure = "closed"
	ConsumeFailureInvalid  ConsumeFailure = "invalid"
)

// Consume enforces the one successful-use rule. An already verified challenge
// is an idempotent success; any other terminal state cannot be retried.
func (c *Challenge) Consume(now time.Time, matches bool) (ConsumeResult, error) {
	now = now.UTC()
	if c.Status == StatusVerified {
		return ConsumeResult{Verified: true, AlreadyVerified: true}, nil
	}
	if c.Status != StatusIssued {
		return ConsumeResult{}, ErrChallengeClosed
	}
	if !now.Before(c.ExpiresAt) {
		c.Status = StatusExpired
		c.ClosedAt = timePointer(now)
		return ConsumeResult{Changed: true}, ErrChallengeExpired
	}
	if matches {
		c.Status = StatusVerified
		c.VerifiedAt = timePointer(now)
		c.ConsumedAt = timePointer(now)
		return ConsumeResult{Verified: true, Changed: true}, nil
	}
	c.AttemptCount++
	if c.AttemptCount >= c.MaxAttempts {
		c.Status = StatusFailed
		c.ClosedAt = timePointer(now)
	}
	return ConsumeResult{Changed: true}, ErrVerificationFailed
}

func (c *Challenge) Revoke(now time.Time) error {
	if c.Status != StatusIssued {
		return ErrChallengeClosed
	}
	c.Status = StatusRevoked
	c.ClosedAt = timePointer(now.UTC())
	return nil
}

func (c *Challenge) Expire(now time.Time) error {
	if c.Status == StatusExpired {
		return nil
	}
	if c.Status != StatusIssued {
		return ErrChallengeClosed
	}
	c.Status = StatusExpired
	c.ClosedAt = timePointer(now.UTC())
	return nil
}

func (c Challenge) IsTerminal() bool {
	return c.Status == StatusVerified || c.Status == StatusFailed || c.Status == StatusExpired || c.Status == StatusRevoked
}

func (c Challenge) Validate() error {
	if c.ID == uuid.Nil || c.SubjectID == uuid.Nil || !validSubjectType(c.SubjectType) || !validPurpose(c.Purpose) || !validMethod(c.Method) || !validChannel(c.Channel) || c.Destination == "" || len(c.CodeHash) != 32 || c.VerifierKeyVersion <= 0 || c.MaxAttempts <= 0 || c.MaxSends <= 0 || c.SendCount < 0 || c.SendCount > c.MaxSends || c.AttemptCount < 0 || c.AttemptCount > c.MaxAttempts || c.NextSendAt.IsZero() || c.ExpiresAt.IsZero() || c.CreatedAt.IsZero() || c.Version < 0 {
		return ErrInvalidChallenge
	}
	if !c.ExpiresAt.After(c.CreatedAt) {
		return fmt.Errorf("%w: expiry must follow creation", ErrInvalidChallenge)
	}
	switch c.Status {
	case StatusIssued:
		if c.AttemptCount >= c.MaxAttempts || c.ConsumedAt != nil || c.VerifiedAt != nil || c.ClosedAt != nil {
			return fmt.Errorf("%w: issued state is inconsistent", ErrInvalidChallenge)
		}
	case StatusVerified:
		if c.VerifiedAt == nil || c.ConsumedAt == nil || c.ClosedAt != nil {
			return fmt.Errorf("%w: verified timestamps are required", ErrInvalidChallenge)
		}
	case StatusFailed, StatusExpired, StatusRevoked:
		if c.ClosedAt == nil || c.VerifiedAt != nil || c.ConsumedAt != nil {
			return fmt.Errorf("%w: terminal state is inconsistent", ErrInvalidChallenge)
		}
	default:
		return ErrInvalidChallenge
	}
	return nil
}

type VirtualStatus string

const (
	VirtualPending   VirtualStatus = "pending"
	VirtualReady     VirtualStatus = "ready"
	VirtualDestroyed VirtualStatus = "destroyed"
)

// VirtualProjection is a dev/test-only encrypted message projection. The
// plaintext verification code never belongs in this type.
type VirtualProjection struct {
	ChallengeID       uuid.UUID
	Channel           Channel
	ChallengeVersion  int64
	CodeCiphertext    []byte
	CodeKeyID         string
	MaskedDestination string
	Status            VirtualStatus
	ExpiresAt         time.Time
	DestroyedAt       *time.Time
	CreatedAt         time.Time
}

// DeliveryPayload is the encrypted, worker-facing material for a provider
// adapter. It is intentionally separate from the public outbox event: the
// event carries only this opaque delivery ID, while a trusted worker decrypts
// the payload immediately before calling an Email/SMS port.
type DeliveryPayload struct {
	ID           uuid.UUID
	ChallengeID  uuid.UUID
	SendSequence int16
	Ciphertext   []byte
	KeyID        string
	AADHash      []byte
	ExpiresAt    time.Time
}

func (p DeliveryPayload) Validate() error {
	if p.ID == uuid.Nil || p.ChallengeID == uuid.Nil || p.SendSequence <= 0 || len(p.Ciphertext) == 0 || p.KeyID == "" || len(p.AADHash) != 32 || p.ExpiresAt.IsZero() {
		return ErrInvalidChallenge
	}
	return nil
}

func (p VirtualProjection) Validate() error {
	if p.ChallengeID == uuid.Nil || !validChannel(p.Channel) || p.ChallengeVersion < 0 || p.MaskedDestination == "" || p.ExpiresAt.IsZero() || p.CreatedAt.IsZero() {
		return ErrInvalidChallenge
	}
	switch p.Status {
	case VirtualPending:
	case VirtualReady:
		if len(p.CodeCiphertext) == 0 || p.CodeKeyID == "" {
			return ErrInvalidChallenge
		}
	case VirtualDestroyed:
		if p.DestroyedAt == nil || len(p.CodeCiphertext) != 0 {
			return ErrInvalidChallenge
		}
	default:
		return ErrInvalidChallenge
	}
	return nil
}

func validSubjectType(value SubjectType) bool {
	switch value {
	case SubjectRegistration, SubjectPasswordReset, SubjectIdentityLink, SubjectPhoneSignIn, SubjectPhoneChange:
		return true
	default:
		return false
	}
}

func validPurpose(value Purpose) bool {
	switch value {
	case PurposeSignupEmail, PurposeSignupPhone, PurposePhoneSignIn, PurposePasswordReset, PurposePhoneChange, PurposeIdentityLink:
		return true
	default:
		return false
	}
}

func validChannel(value Channel) bool {
	return value == ChannelEmailCode || value == ChannelSMSCode
}

func validMethod(value Method) bool {
	return value == MethodEmail || value == MethodPhone
}

func copyBytes(value []byte) []byte { return append([]byte(nil), value...) }

func copyUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func timePointer(value time.Time) *time.Time {
	copy := value.UTC()
	return &copy
}
