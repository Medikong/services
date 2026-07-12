package registration

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPendingVerification Status = "pending_verification"
	StatusAwaitingUserLink    Status = "awaiting_user_link"
	StatusLinked              Status = "linked"
	StatusIssuingSession      Status = "issuing_session"
	StatusCompleted           Status = "completed"
	StatusFailed              Status = "failed"
	StatusExpired             Status = "expired"
)

type Method string

const (
	MethodEmail Method = "email"
	MethodPhone Method = "phone"
)

var (
	ErrInvalidRegistration    = errors.New("invalid registration")
	ErrInvalidTransition      = errors.New("invalid registration status transition")
	ErrVerificationIncomplete = errors.New("registration verification is incomplete")
	ErrLinkConflict           = errors.New("registration link request conflicts with existing state")
	ErrSessionDeadlineElapsed = errors.New("registration session delivery deadline elapsed")
)

// Registration is the Auth-owned process state for a signup. It contains only
// opaque references; profile values and user creation stay outside Auth.
type Registration struct {
	ID              uuid.UUID
	IntentID        uuid.UUID
	EmailIdentityID uuid.UUID
	PhoneIdentityID uuid.UUID

	EmailChallengeID *uuid.UUID
	PhoneChallengeID *uuid.UUID

	ProfileRequestID    string
	AgreementReceiptID  string
	RememberMe          bool
	ClientChannel       string
	Status              Status
	VerifiedMethods     []Method
	StatusTokenHash     []byte
	StatusTokenKeyVer   int16
	StatusTokenExpires  time.Time
	VerificationBinding *uuid.UUID
	VerificationVersion *int64
	VerificationHash    []byte
	VerificationEventID *uuid.UUID

	LinkRequestID                 *uuid.UUID
	CompletionIdempotencyRecordID *uuid.UUID
	UserID                        *uuid.UUID
	LinkedAt                      *time.Time
	SessionID                     *uuid.UUID
	SessionPolicyVersion          *int64
	LinkAcceptUntil               *time.Time
	SessionIssueUntil             *time.Time
	FailureCode                   *string
	ExpiresAt                     time.Time
	CompletedAt                   *time.Time
	Version                       int64
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
}

type NewInput struct {
	ID                 uuid.UUID
	IntentID           uuid.UUID
	EmailIdentityID    uuid.UUID
	PhoneIdentityID    uuid.UUID
	ProfileRequestID   string
	AgreementReceiptID string
	RememberMe         bool
	ClientChannel      string
	StatusTokenHash    []byte
	StatusTokenKeyVer  int16
	StatusTokenExpires time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

func New(input NewInput) (Registration, error) {
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	registration := Registration{
		ID:                 input.ID,
		IntentID:           input.IntentID,
		EmailIdentityID:    input.EmailIdentityID,
		PhoneIdentityID:    input.PhoneIdentityID,
		ProfileRequestID:   input.ProfileRequestID,
		AgreementReceiptID: input.AgreementReceiptID,
		RememberMe:         input.RememberMe,
		ClientChannel:      input.ClientChannel,
		Status:             StatusPendingVerification,
		StatusTokenHash:    copyBytes(input.StatusTokenHash),
		StatusTokenKeyVer:  input.StatusTokenKeyVer,
		StatusTokenExpires: input.StatusTokenExpires.UTC(),
		ExpiresAt:          input.ExpiresAt.UTC(),
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
	}
	if err := registration.Validate(); err != nil {
		return Registration{}, err
	}
	return registration, nil
}

func (r *Registration) AttachChallenge(method Method, challengeID uuid.UUID) error {
	if r.Status != StatusPendingVerification || challengeID == uuid.Nil {
		return ErrInvalidTransition
	}
	switch method {
	case MethodEmail:
		r.EmailChallengeID = uuidPointer(challengeID)
	case MethodPhone:
		r.PhoneChallengeID = uuidPointer(challengeID)
	default:
		return fmt.Errorf("%w: unsupported challenge method", ErrInvalidRegistration)
	}
	return nil
}

// MarkMethodVerified records a successfully consumed ownership proof without
// moving the Registration into the user-link phase. Both methods must be
// verified before Complete can create the cross-context link request.
func (r *Registration) MarkMethodVerified(method Method) error {
	if r.Status != StatusPendingVerification {
		return ErrInvalidTransition
	}
	if method != MethodEmail && method != MethodPhone {
		return fmt.Errorf("%w: unsupported verification method", ErrInvalidRegistration)
	}
	for _, verified := range r.VerifiedMethods {
		if verified == method {
			return nil
		}
	}
	r.VerifiedMethods = append(r.VerifiedMethods, method)
	if len(r.VerifiedMethods) == 2 && r.VerifiedMethods[0] == MethodPhone {
		r.VerifiedMethods[0], r.VerifiedMethods[1] = r.VerifiedMethods[1], r.VerifiedMethods[0]
	}
	return nil
}

func (r Registration) MethodVerified(method Method) bool {
	for _, verified := range r.VerifiedMethods {
		if verified == method {
			return true
		}
	}
	return false
}

type VerificationCompletion struct {
	EmailChallengeID           uuid.UUID
	PhoneChallengeID           uuid.UUID
	EmailVerified              bool
	PhoneVerified              bool
	BindingID                  uuid.UUID
	RegistrationVersion        int64
	SnapshotHash               []byte
	VerificationCompletedEvent uuid.UUID
	CompletionIdempotencyID    uuid.UUID
	LinkAcceptUntil            time.Time
}

// MarkVerificationCompleted fixes the one immutable verification snapshot used
// by the asynchronous user-link operation.
func (r *Registration) MarkVerificationCompleted(input VerificationCompletion) error {
	if r.Status == StatusAwaitingUserLink && r.matchesVerificationCompletion(input) {
		return nil
	}
	if r.Status != StatusPendingVerification {
		return ErrInvalidTransition
	}
	if !input.EmailVerified || !input.PhoneVerified || r.EmailChallengeID == nil || r.PhoneChallengeID == nil || *r.EmailChallengeID != input.EmailChallengeID || *r.PhoneChallengeID != input.PhoneChallengeID {
		return ErrVerificationIncomplete
	}
	if input.BindingID == uuid.Nil || input.VerificationCompletedEvent == uuid.Nil || input.CompletionIdempotencyID == uuid.Nil || input.RegistrationVersion <= 0 || len(input.SnapshotHash) != 32 || input.LinkAcceptUntil.IsZero() || input.LinkAcceptUntil.After(r.ExpiresAt) {
		return fmt.Errorf("%w: invalid verification snapshot", ErrInvalidRegistration)
	}
	r.Status = StatusAwaitingUserLink
	r.VerifiedMethods = []Method{MethodEmail, MethodPhone}
	r.VerificationBinding = uuidPointer(input.BindingID)
	r.VerificationVersion = int64Pointer(input.RegistrationVersion)
	r.VerificationHash = copyBytes(input.SnapshotHash)
	r.VerificationEventID = uuidPointer(input.VerificationCompletedEvent)
	r.CompletionIdempotencyRecordID = uuidPointer(input.CompletionIdempotencyID)
	r.LinkAcceptUntil = timePointer(input.LinkAcceptUntil.UTC())
	return nil
}

type UserLink struct {
	UserID            uuid.UUID
	LinkRequestID     uuid.UUID
	LinkedAt          time.Time
	SessionIssueUntil time.Time
}

// Link records the externally-issued user_id. It never creates or changes one.
func (r *Registration) Link(input UserLink) error {
	if r.Status == StatusLinked && sameUUID(r.UserID, input.UserID) && sameUUID(r.LinkRequestID, input.LinkRequestID) {
		return nil
	}
	if r.Status != StatusAwaitingUserLink {
		return ErrInvalidTransition
	}
	if input.UserID == uuid.Nil || input.LinkRequestID == uuid.Nil || input.LinkedAt.IsZero() || input.SessionIssueUntil.IsZero() || input.SessionIssueUntil.After(r.ExpiresAt) {
		return fmt.Errorf("%w: invalid user link", ErrInvalidRegistration)
	}
	if r.LinkAcceptUntil == nil || input.LinkedAt.After(*r.LinkAcceptUntil) {
		return ErrSessionDeadlineElapsed
	}
	r.Status = StatusLinked
	r.UserID = uuidPointer(input.UserID)
	r.LinkRequestID = uuidPointer(input.LinkRequestID)
	r.LinkedAt = timePointer(input.LinkedAt.UTC())
	r.SessionIssueUntil = timePointer(input.SessionIssueUntil.UTC())
	return nil
}

func (r *Registration) BeginSessionIssuance(now time.Time) error {
	if r.Status == StatusIssuingSession {
		return nil
	}
	if r.Status != StatusLinked {
		return ErrInvalidTransition
	}
	if r.SessionIssueUntil == nil || !now.UTC().Before(*r.SessionIssueUntil) {
		return ErrSessionDeadlineElapsed
	}
	r.Status = StatusIssuingSession
	return nil
}

func (r *Registration) Complete(sessionID uuid.UUID, completedAt time.Time) error {
	if r.Status == StatusCompleted && sameUUID(r.SessionID, sessionID) {
		return nil
	}
	if r.Status != StatusIssuingSession || sessionID == uuid.Nil || completedAt.IsZero() {
		return ErrInvalidTransition
	}
	if r.SessionIssueUntil == nil || completedAt.UTC().After(*r.SessionIssueUntil) {
		return ErrSessionDeadlineElapsed
	}
	r.Status = StatusCompleted
	r.SessionID = uuidPointer(sessionID)
	r.CompletedAt = timePointer(completedAt.UTC())
	return nil
}

func (r *Registration) Fail(code string) error {
	if r.IsTerminal() || code == "" {
		return ErrInvalidTransition
	}
	r.Status = StatusFailed
	r.FailureCode = stringPointer(code)
	return nil
}

func (r *Registration) Expire(code string) error {
	if r.IsTerminal() || code == "" {
		return ErrInvalidTransition
	}
	r.Status = StatusExpired
	r.FailureCode = stringPointer(code)
	return nil
}

func (r Registration) IsTerminal() bool {
	return r.Status == StatusCompleted || r.Status == StatusFailed || r.Status == StatusExpired
}

func (r Registration) Validate() error {
	if r.ID == uuid.Nil || r.IntentID == uuid.Nil || r.EmailIdentityID == uuid.Nil || r.PhoneIdentityID == uuid.Nil || r.EmailIdentityID == r.PhoneIdentityID || r.ProfileRequestID == "" || r.AgreementReceiptID == "" || !validClientChannel(r.ClientChannel) || !validVerifiedMethods(r.VerifiedMethods) || len(r.StatusTokenHash) != 32 || r.StatusTokenKeyVer <= 0 || r.ExpiresAt.IsZero() || r.StatusTokenExpires.IsZero() || r.Version < 0 || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return ErrInvalidRegistration
	}
	if r.StatusTokenExpires.Before(r.ExpiresAt) {
		return fmt.Errorf("%w: status proof must outlive registration", ErrInvalidRegistration)
	}
	switch r.Status {
	case StatusPendingVerification:
	case StatusAwaitingUserLink:
		if r.EmailChallengeID == nil || r.PhoneChallengeID == nil || r.VerificationBinding == nil || r.VerificationVersion == nil || *r.VerificationVersion <= 0 || r.VerificationHash == nil || len(r.VerificationHash) != 32 || r.VerificationEventID == nil || r.CompletionIdempotencyRecordID == nil || r.LinkAcceptUntil == nil {
			return ErrInvalidRegistration
		}
	case StatusLinked, StatusIssuingSession:
		if r.UserID == nil || r.LinkRequestID == nil || r.LinkedAt == nil || r.SessionIssueUntil == nil {
			return ErrInvalidRegistration
		}
	case StatusCompleted:
		if r.UserID == nil || r.LinkRequestID == nil || r.SessionID == nil || r.CompletedAt == nil {
			return ErrInvalidRegistration
		}
	case StatusFailed, StatusExpired:
		if r.FailureCode == nil || *r.FailureCode == "" {
			return ErrInvalidRegistration
		}
	default:
		return ErrInvalidRegistration
	}
	return nil
}

func (r Registration) matchesVerificationCompletion(input VerificationCompletion) bool {
	return r.VerificationBinding != nil && *r.VerificationBinding == input.BindingID && r.VerificationVersion != nil && *r.VerificationVersion == input.RegistrationVersion && bytes.Equal(r.VerificationHash, input.SnapshotHash) && r.VerificationEventID != nil && *r.VerificationEventID == input.VerificationCompletedEvent && r.CompletionIdempotencyRecordID != nil && *r.CompletionIdempotencyRecordID == input.CompletionIdempotencyID
}

func copyBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }
func int64Pointer(value int64) *int64        { return &value }
func timePointer(value time.Time) *time.Time { return &value }
func stringPointer(value string) *string     { return &value }

func sameUUID(value *uuid.UUID, expected uuid.UUID) bool {
	return value != nil && *value == expected
}

func validClientChannel(value string) bool {
	return value == "web" || value == "mobile"
}

func validVerifiedMethods(values []Method) bool {
	seen := make(map[Method]struct{}, len(values))
	for _, value := range values {
		if value != MethodEmail && value != MethodPhone {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
