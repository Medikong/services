package principal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/Medikong/services/packages/go-authz/rbac"
)

type Type string

const (
	TypeAnonymous Type = "anonymous"
	TypeUser      Type = "user"
	TypeService   Type = "service"
)

type Principal struct {
	Type        Type     `json:"type"`
	UserID      string   `json:"userId,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	AuthMethods []string `json:"authMethods,omitempty"`
	AuthLevel   string   `json:"authLevel,omitempty"`
	SessionID   string   `json:"sessionId,omitempty"`
	ClientType  string   `json:"clientType,omitempty"`
	DeviceID    string   `json:"deviceId,omitempty"`
}

func Anonymous() Principal {
	return Principal{Type: TypeAnonymous}
}

func EncodeHeader(p Principal) (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func DecodeHeader(value string) (Principal, error) {
	if value == "" {
		return Principal{}, fmt.Errorf("principal header is empty")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Principal{}, err
	}
	var p Principal
	if err := json.Unmarshal(data, &p); err != nil {
		return Principal{}, err
	}
	if p.Type == "" {
		return Principal{}, fmt.Errorf("principal type is empty")
	}
	return p, nil
}

func (p Principal) HasRole(role string) bool {
	want, canonicalWant := rbac.Canonical(role)
	for _, candidate := range p.Roles {
		if candidate == role {
			return true
		}
		if got, ok := rbac.Canonical(candidate); ok && canonicalWant && got == want {
			return true
		}
	}
	return false
}
