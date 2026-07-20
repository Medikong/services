package sessionprojection

import (
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

type ClaimedChange struct {
	JobID uuid.UUID
	domainsession.StatusChange
	Attempts int
}

type Config struct {
	WorkerID     string
	BatchSize    int
	PollInterval time.Duration
	Lease        time.Duration
	ApplyTimeout time.Duration
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
	OnAttempt    func(string)
}

type Result struct {
	Claimed int
	Applied int
	Retried int
	Expired int
}
