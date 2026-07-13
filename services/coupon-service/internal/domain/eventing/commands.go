package eventing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type CommandRequest struct {
	ID                uuid.UUID
	CommandDocumentID string
	PolicyDocumentID  string
	SourceEventID     *uuid.UUID
	AggregateType     string
	AggregateID       string
	BusinessKey       string
	CorrelationID     string
	CausationID       string
	TraceID           string
	Payload           json.RawMessage
	AttemptCount      int
	LeaseOwner        string
	LeaseUntil        time.Time
}

// CommandSubmission is the transport-neutral inbound contract for commands
// supplied by an external operations or failure-recording adapter.
type CommandSubmission struct {
	ID                uuid.UUID
	CommandDocumentID string
	AggregateType     string
	AggregateID       string
	BusinessKey       string
	CorrelationID     string
	CausationID       string
	TraceID           string
	Payload           json.RawMessage
	NotBefore         time.Time
}

type CommandSubmitter interface {
	SubmitCommand(context.Context, CommandSubmission) (uuid.UUID, error)
}

type CommandQueue interface {
	ClaimCommands(context.Context, string, int, time.Duration) ([]CommandRequest, error)
	CompleteCommand(context.Context, uuid.UUID, string, string) error
	FailCommand(context.Context, uuid.UUID, string, time.Time, string, bool) error
}
