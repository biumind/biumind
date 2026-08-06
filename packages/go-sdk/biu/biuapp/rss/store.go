// In-memory subscription store.
//
// v1.5 keeps subscriptions in process memory — fine for the agent
// loop's "fetch a feed and summarize" path that doesn't need durable
// storage, and lets the App ship without depending on Brain.Wiki at
// install time.
//
// v2.0 will replace this with a Brain.Wiki-backed store so
// subscriptions survive restarts and sync across devices via the
// existing wiki collection model. The current contract (Add / List /
// Remove / Get) is what that swap will preserve.

package rss

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// Subscription is one feed the user has added.
type Subscription struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Tags      []string  `json:"tags,omitempty"`
	AddedAt   time.Time `json:"added_at"`
	LastFetch time.Time `json:"last_fetch,omitempty"`
	Unread    int       `json:"unread"`
}

// store is the App-level subscription store. Goroutine-safe.
type store struct {
	mu   sync.RWMutex
	subs map[string]*Subscription // id → sub
}

func newStore() *store { return &store{subs: map[string]*Subscription{}} }

// Add returns the existing entry when url already exists (idempotent
// — re-adding the same URL is a common user mistake; better to be
// silent than to refuse).
func (s *store) Add(url, title string, tags []string) *Subscription {
	id := subscriptionID(url)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.subs[id]; ok {
		return existing
	}
	sub := &Subscription{
		ID:      id,
		URL:     url,
		Title:   title,
		Tags:    tags,
		AddedAt: time.Now().UTC(),
	}
	s.subs[id] = sub
	return sub
}

func (s *store) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[id]; !ok {
		return false
	}
	delete(s.subs, id)
	return true
}

func (s *store) Get(id string) (*Subscription, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subs[id]
	return sub, ok
}

// List returns subscriptions sorted by AddedAt descending so the
// freshly-added one shows up first.
func (s *store) List() []*Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out
}

// MarkFetched stamps last_fetch and updates unread (caller computes).
// Returns false when the id isn't known.
func (s *store) MarkFetched(id string, unread int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[id]
	if !ok {
		return false
	}
	sub.LastFetch = time.Now().UTC()
	sub.Unread = unread
	return true
}

// subscriptionID hashes the URL into a stable short id. We avoid uuid
// here so re-adding the same URL is naturally idempotent (same id
// every time).
func subscriptionID(url string) string {
	sum := sha256.Sum256([]byte(url))
	return "sub_" + hex.EncodeToString(sum[:8])
}
