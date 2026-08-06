// Package cache implements a thread-safe LRU decision cache with TTL.
//
// Cache key = (principal_uid, action, resource_uid, ctx_hash)
// TTL is short (default 30s) — policies hot-reload invalidates entire cache via Clear.
package cache

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Decision mirrors engine.Decision but kept primitive for cacheability.
type Decision uint8

const (
	DecisionUnspecified Decision = 0
	DecisionAllow       Decision = 1
	DecisionDeny        Decision = 2
)

type Entry struct {
	Decision        Decision
	Reason          string
	MatchedPolicies []string
	ExpiresAt       time.Time
}

type DecisionCache struct {
	mu  sync.Mutex
	c   *lru.Cache[string, Entry]
	ttl time.Duration
}

func New(size int, ttl time.Duration) (*DecisionCache, error) {
	c, err := lru.New[string, Entry](size)
	if err != nil {
		return nil, err
	}
	return &DecisionCache{c: c, ttl: ttl}, nil
}

// Get returns (entry, true) if hit & not expired.
func (d *DecisionCache) Get(key string) (Entry, bool) {
	e, ok := d.c.Get(key)
	if !ok {
		return Entry{}, false
	}
	if time.Now().After(e.ExpiresAt) {
		d.c.Remove(key)
		return Entry{}, false
	}
	return e, true
}

func (d *DecisionCache) Set(key string, e Entry) {
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = time.Now().Add(d.ttl)
	}
	d.c.Add(key, e)
}

// Clear flushes everything (call on policy reload).
func (d *DecisionCache) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.c.Purge()
}

func (d *DecisionCache) Len() int { return d.c.Len() }
