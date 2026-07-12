package access

import "github.com/google/uuid"

type State struct {
	UserID             uuid.UUID
	Status             string
	RestrictionVersion int64
}

type Grant struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Roles       []string
	Permissions []string
	Version     int64
	Status      string
}
