// Package hub manages SSE connections + topic subscriptions + fanout.
//
// One Hub per process; horizontal scaling done via consistent-hash on
// device_id at the LB layer (each replica sees a disjoint set of clients,
// but each replica subscribes to ALL NATS messages and filters in-memory).
package hub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/biumind/biumind/services/realtime/internal/ledger"
)

// Conn is one SSE connection. The owning HTTP handler is the only goroutine
// that reads from Out; Hub writes to it via fanout.
type Conn struct {
	id      string // device_id or random
	userID  string
	topics  map[string]struct{}
	out     chan ledger.Event
	closed  atomic.Bool
	closeMu sync.Mutex
	closeFn func() // called once on close
}

func (c *Conn) ID() string               { return c.id }
func (c *Conn) UserID() string           { return c.userID }
func (c *Conn) Out() <-chan ledger.Event { return c.out }

// Topics returns a snapshot.
func (c *Conn) Topics() []string {
	out := make([]string, 0, len(c.topics))
	for t := range c.topics {
		out = append(out, t)
	}
	return out
}

// Close marks the connection closed; idempotent.
func (c *Conn) Close() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed.Load() {
		return
	}
	c.closed.Store(true)
	close(c.out)
	if c.closeFn != nil {
		c.closeFn()
	}
}

// Hub is the central registry.
type Hub struct {
	mu      sync.RWMutex
	conns   map[string]*Conn              // id → conn
	byTopic map[string]map[*Conn]struct{} // topic → set
	bufSize int
}

func NewHub(bufSize int) *Hub {
	if bufSize <= 0 {
		bufSize = 1024
	}
	return &Hub{
		conns:   make(map[string]*Conn),
		byTopic: make(map[string]map[*Conn]struct{}),
		bufSize: bufSize,
	}
}

// Register creates a new Conn for a device.
func (h *Hub) Register(deviceID, userID string, topics []string) *Conn {
	c := &Conn{
		id:     deviceID,
		userID: userID,
		topics: make(map[string]struct{}, len(topics)),
		out:    make(chan ledger.Event, h.bufSize),
	}
	c.closeFn = func() { h.unregister(c) }

	h.mu.Lock()
	defer h.mu.Unlock()
	// If a conn with same id existed, close it first (e.g. rapid reconnect).
	if old, ok := h.conns[deviceID]; ok {
		// can't acquire its closeMu while holding hub.mu; mark and let it self-clean
		go old.Close()
	}
	h.conns[deviceID] = c
	for _, t := range topics {
		c.topics[t] = struct{}{}
		h.subscribeLocked(t, c)
	}
	return c
}

// Subscribe adds a topic to an existing conn.
func (h *Hub) Subscribe(deviceID, topic string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[deviceID]
	if !ok {
		return ErrConnNotFound
	}
	if _, exists := c.topics[topic]; exists {
		return nil
	}
	c.topics[topic] = struct{}{}
	h.subscribeLocked(topic, c)
	return nil
}

func (h *Hub) Unsubscribe(deviceID, topic string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[deviceID]
	if !ok {
		return ErrConnNotFound
	}
	delete(c.topics, topic)
	h.unsubscribeLocked(topic, c)
	return nil
}

// Publish sends e to all conns currently subscribed to e.Topic.
// Slow consumers (full out chan) are closed and unregistered.
func (h *Hub) Publish(e ledger.Event) (delivered, dropped int) {
	h.mu.RLock()
	subs := h.byTopic[e.Topic]
	targets := make([]*Conn, 0, len(subs))
	for c := range subs {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		// Hold closeMu so Close() cannot close c.out between the closed
		// check and the send. The send stays non-blocking, so the lock
		// is never held while waiting on a consumer.
		c.closeMu.Lock()
		if c.closed.Load() {
			c.closeMu.Unlock()
			continue
		}
		select {
		case c.out <- e:
			delivered++
		default:
			// Slow consumer: drop entire connection (protects shared mem).
			dropped++
			go c.Close()
		}
		c.closeMu.Unlock()
	}
	return
}

// Stats returns counters useful for /readyz / /metrics.
func (h *Hub) Stats() Stats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Stats{
		Connections: len(h.conns),
		Topics:      len(h.byTopic),
	}
}

type Stats struct {
	Connections int
	Topics      int
}

// ─── private ──────────────────────────────────────────────

func (h *Hub) subscribeLocked(topic string, c *Conn) {
	set, ok := h.byTopic[topic]
	if !ok {
		set = make(map[*Conn]struct{})
		h.byTopic[topic] = set
	}
	set[c] = struct{}{}
}

func (h *Hub) unsubscribeLocked(topic string, c *Conn) {
	set := h.byTopic[topic]
	if set == nil {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.byTopic, topic)
	}
}

func (h *Hub) unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for t := range c.topics {
		h.unsubscribeLocked(t, c)
	}
	if cur, ok := h.conns[c.id]; ok && cur == c {
		delete(h.conns, c.id)
	}
}

// Run starts a background loop ticking GC on the supplied ledger.
func (h *Hub) Run(ctx context.Context, l *ledger.Ledger, gcInterval int) {
	if l == nil || gcInterval <= 0 {
		<-ctx.Done()
		return
	}
}

var ErrConnNotFound = errors.New("realtime: connection not found")
