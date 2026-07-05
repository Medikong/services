package service

import (
	"sync"

	"github.com/Medikong/services/packages/go-authz/principal"
)

type MemoryAuthzCache struct {
	mu      sync.RWMutex
	entries map[string]principal.Principal
}

func NewMemoryAuthzCache() *MemoryAuthzCache {
	return &MemoryAuthzCache{entries: map[string]principal.Principal{}}
}

func (c *MemoryAuthzCache) Get(accessToken string) (principal.Principal, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.entries[accessToken]
	return p, ok
}

func (c *MemoryAuthzCache) Set(accessToken string, p principal.Principal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[accessToken] = p
}

func (c *MemoryAuthzCache) Delete(accessToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, accessToken)
}
