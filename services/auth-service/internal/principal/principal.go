package principal

import (
	"context"
	"sync"

	authzprincipal "github.com/Medikong/services/packages/go-authz/principal"
)

type Input struct {
	SessionID     string
	AuthAccountID string
	UserID        string
	AuthMethods   []string
}

type AuthResult struct {
	AuthAccountID   string                   `json:"authAccountId"`
	UserID          string                   `json:"userId"`
	AccessToken     string                   `json:"accessToken"`
	RefreshToken    string                   `json:"refreshToken"`
	Principal       authzprincipal.Principal `json:"principal"`
	PrincipalHeader string                   `json:"principalHeader"`
}

type RoleRepository interface {
	ListByAuthAccountID(ctx context.Context, authAccountID string) ([]string, error)
}

type Builder struct {
	roles RoleRepository
}

func NewBuilder(roles RoleRepository) Builder {
	return Builder{roles: roles}
}

func (b Builder) Build(ctx context.Context, input Input) (authzprincipal.Principal, string, error) {
	roles, err := b.roles.ListByAuthAccountID(ctx, input.AuthAccountID)
	if err != nil {
		return authzprincipal.Principal{}, "", err
	}
	p := authzprincipal.Principal{
		Type:        authzprincipal.TypeUser,
		UserID:      input.UserID,
		Roles:       append([]string(nil), roles...),
		AuthMethods: append([]string(nil), input.AuthMethods...),
		AuthLevel:   "normal",
		SessionID:   input.SessionID,
		ClientType:  "api",
	}
	header, err := authzprincipal.EncodeHeader(p)
	if err != nil {
		return authzprincipal.Principal{}, "", err
	}
	return p, header, nil
}

type AuthzCache interface {
	Get(accessToken string) (authzprincipal.Principal, bool)
	Set(accessToken string, p authzprincipal.Principal)
	Delete(accessToken string)
}

type MemoryAuthzCache struct {
	mu      sync.RWMutex
	entries map[string]authzprincipal.Principal
}

func NewMemoryAuthzCache() *MemoryAuthzCache {
	return &MemoryAuthzCache{entries: map[string]authzprincipal.Principal{}}
}

func (c *MemoryAuthzCache) Get(accessToken string) (authzprincipal.Principal, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.entries[accessToken]
	return p, ok
}

func (c *MemoryAuthzCache) Set(accessToken string, p authzprincipal.Principal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[accessToken] = p
}

func (c *MemoryAuthzCache) Delete(accessToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, accessToken)
}
