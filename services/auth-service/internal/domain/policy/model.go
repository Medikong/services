package policy

import (
	"encoding/json"
	"time"
)

type Snapshot struct {
	Name        string
	Version     int64
	Status      string
	Rules       json.RawMessage
	EffectiveAt time.Time
}

// GlobalSnapshot is the immutable document exposed by the operator API. The
// individual policy rows remain the execution records referenced by existing
// authentication data, while this aggregate preserves one version for the
// complete operator-visible decision surface.
type GlobalSnapshot struct {
	Version     int64
	Status      string
	Document    json.RawMessage
	EffectiveAt time.Time
}
