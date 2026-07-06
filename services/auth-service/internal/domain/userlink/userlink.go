package userlink

import (
	"time"
)

type Link struct {
	AuthUserLinkID int64
	AuthAccountID  string
	UserID         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
