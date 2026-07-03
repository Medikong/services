package acl

import "time"

type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

type SubjectRef struct {
	Type string
	ID   string
}

type ResourceRef struct {
	Type string
	ID   string
}

type Override struct {
	ID        string
	Subject   SubjectRef
	Resource  ResourceRef
	Action    string
	Effect    Effect
	ExpiresAt *time.Time
}
