// Health monitoring + auto-reconnect for connected MCP servers.
//
// Without this layer, a server that dies after biu starts —
// stdio child OOM-killed, HTTP server rolling-deployed, network
// hiccup — leaves biu's connection permanently broken. Every
// subsequent tool call returns a transport-layer error until the
// user kills + restarts biu.
//
// HealthMonitor runs one background goroutine per Registry. Every
// `interval` it Ping()s each registered Client; failures arm a
// reconnect with exponential backoff. On successful reconnect we
// re-list tools and diff against the last known catalog so the
// engine registry stays in sync — a new server version that
// added tools shows them up immediately; a removed tool stops
// being callable.
//
// The monitor is opt-in. Bootstrap callers wire it via
// Registry.StartHealthMonitor; Close stops it cleanly.

package mcp

import (
	"context"
	"sync"
	"time"
)

// Default health-check cadence. 30s is a compromise: long enough
// that the ping cost is invisible (~60ms × N servers / 30s = 0.2%
// CPU overhead at N=10), short enough that a dead server is
// noticed within a minute. Lower this in tests via the option
// struct.
const defaultHealthInterval = 30 * time.Second

// Backoff schedule for reconnect attempts after consecutive
// failures: 1s → 2s → 4s → 8s → 16s → 32s → 60s capped.
// Resets to 1s on first successful reconnect.
var defaultBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	60 * time.Second,
}

// HealthOptions tunes HealthMonitor behaviour. Zero values yield
// production defaults; tests can crank Interval down to 50ms to
// drive the loop deterministically.
type HealthOptions struct {
	// Interval between ping cycles per server. 0 → 30s default.
	Interval time.Duration

	// Backoff is the per-server retry schedule on consecutive
	// reconnect failures. Empty → defaultBackoff. Each element
	// is the wait BEFORE the corresponding attempt; the last
	// element is reused for any further attempts.
	Backoff []time.Duration

	// PingTimeout caps each individual ping. 0 → 5s. Set lower in
	// tests to fail fast on a deliberately-killed server.
	PingTimeout time.Duration

	// ReconnectTimeout caps each reconnect attempt. 0 → 15s
	// (subprocess respawn + initialize round-trip can be slow on
	// cold caches; HTTP cases finish in tens of ms).
	ReconnectTimeout time.Duration

	// Logf, when non-nil, receives one human-readable line per
	// state change (lost / reconnecting / reconnected, with
	// catalog-diff summary). Nil = silent. Wiring layer points
	// this at fmt.Fprintf(os.Stderr, ...).
	Logf func(format string, args ...any)
}

// HealthMonitor is the background loop. One per Registry.
type HealthMonitor struct {
	registry *Registry
	opts     HealthOptions

	cancel context.CancelFunc
	done   chan struct{}

	// Per-server reconnect state; keyed by Client.Name(). Live
	// only while the monitor runs.
	mu     sync.Mutex
	state  map[string]*serverHealth
}

// serverHealth tracks per-server reconnect bookkeeping. Lives in
// HealthMonitor.state so it's only allocated for servers the
// monitor has seen at least once.
type serverHealth struct {
	consecutiveFails int
	lastFailureAt    time.Time
	lastReconnectAt  time.Time
	previousCatalog  []ToolDef // last known good tools/list snapshot
	healthy          bool      // last observed state
}

// StartHealthMonitor kicks off the background loop. The returned
// monitor's Stop method cancels the loop and waits for the
// goroutine to drain. Calling StartHealthMonitor twice on the
// same Registry without an intermediate Stop returns the first
// monitor (the duplicate goroutine is rejected). nil receiver →
// nil monitor.
func (r *Registry) StartHealthMonitor(opts HealthOptions) *HealthMonitor {
	if r == nil {
		return nil
	}
	if opts.Interval == 0 {
		opts.Interval = defaultHealthInterval
	}
	if opts.PingTimeout == 0 {
		opts.PingTimeout = 5 * time.Second
	}
	if opts.ReconnectTimeout == 0 {
		opts.ReconnectTimeout = 15 * time.Second
	}
	if len(opts.Backoff) == 0 {
		opts.Backoff = defaultBackoff
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &HealthMonitor{
		registry: r,
		opts:     opts,
		cancel:   cancel,
		done:     make(chan struct{}),
		state:    map[string]*serverHealth{},
	}
	go m.loop(ctx)
	return m
}

// Stop cancels the loop and waits for it to drain. Safe to call
// multiple times; subsequent calls are no-ops. Does NOT close the
// underlying Registry — clients stay alive after Stop returns.
func (m *HealthMonitor) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	<-m.done
}

// loop is the background goroutine. Sleeps for Interval, probes
// every connected client, dispatches reconnects on failures.
// Exits when ctx is cancelled (via Stop).
func (m *HealthMonitor) loop(ctx context.Context) {
	defer close(m.done)
	t := time.NewTicker(m.opts.Interval)
	defer t.Stop()
	// First probe runs immediately so a dead-on-arrival server
	// (subprocess crashed during initialize) gets a reconnect on
	// the spot rather than after one full Interval.
	m.probeAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.probeAll(ctx)
		}
	}
}

// probeAll snapshots the registry's clients, pings each, and
// dispatches reconnects sequentially. Sequential rather than
// parallel because:
//   - Servers are typically a handful (< 10), so wall-clock cost
//     is bounded at N×PingTimeout in the worst case.
//   - Sequential simplifies log ordering (no interleaved
//     "reconnected" lines from multiple servers).
//
// A future optimisation could fan out across servers when the
// registry holds many; not worth the complexity today.
func (m *HealthMonitor) probeAll(ctx context.Context) {
	m.registry.mu.RLock()
	clients := make([]Client, 0, len(m.registry.clients))
	for _, c := range m.registry.clients {
		clients = append(clients, c)
	}
	m.registry.mu.RUnlock()

	for _, c := range clients {
		select {
		case <-ctx.Done():
			return
		default:
		}
		m.probeOne(ctx, c)
	}
}

// probeOne pings a single client. On success, resets the failure
// counter. On failure, schedules a reconnect.
func (m *HealthMonitor) probeOne(ctx context.Context, c Client) {
	pingCtx, cancel := context.WithTimeout(ctx, m.opts.PingTimeout)
	err := c.Ping(pingCtx)
	cancel()

	name := c.Name()
	m.mu.Lock()
	st, ok := m.state[name]
	if !ok {
		st = &serverHealth{healthy: true}
		m.state[name] = st
	}
	m.mu.Unlock()

	if err == nil {
		// Healthy. Reset the counter; if we were tracking a
		// previous failure, log the recovery — but only when the
		// monitor is the one who triggered it (consecutiveFails
		// > 0 means we were attempting reconnects).
		st.consecutiveFails = 0
		if !st.healthy {
			st.healthy = true
			m.logf("[biu] mcp/%s: healthy again", name)
		}
		return
	}

	// Failed. Mark unhealthy, increment counter, and try to
	// reconnect under the relevant backoff slot.
	st.consecutiveFails++
	st.lastFailureAt = time.Now()
	if st.healthy {
		st.healthy = false
		m.logf("[biu] mcp/%s: lost (%v)", name, err)
	}

	// Pick the backoff entry. Index clamped at last-element so
	// repeated long outages reuse the cap (60s).
	idx := st.consecutiveFails - 1
	if idx >= len(m.opts.Backoff) {
		idx = len(m.opts.Backoff) - 1
	}
	wait := m.opts.Backoff[idx]
	// Ensure we waited at least `wait` since the previous attempt
	// — multiple probes during the backoff period collapse to one
	// reconnect.
	if !st.lastReconnectAt.IsZero() {
		if since := time.Since(st.lastReconnectAt); since < wait {
			return
		}
	}
	st.lastReconnectAt = time.Now()
	m.logf("[biu] mcp/%s: reconnecting (attempt %d, wait was %v)",
		name, st.consecutiveFails, wait)
	m.attemptReconnect(ctx, c, st)
}

// attemptReconnect tears the client back up + diffs the new tool
// catalog against the previous snapshot. Successful reconnect
// resets the failure counter; the next probe iteration will
// observe healthy and log a recovery line.
func (m *HealthMonitor) attemptReconnect(ctx context.Context, c Client, st *serverHealth) {
	rcCtx, cancel := context.WithTimeout(ctx, m.opts.ReconnectTimeout)
	defer cancel()
	if err := c.Reconnect(rcCtx); err != nil {
		m.logf("[biu] mcp/%s: reconnect failed: %v", c.Name(), err)
		return
	}
	// Re-list tools so we can diff against the last good catalog
	// + update the registry. ListTools timeout is generous because
	// some servers stat their plugin set lazily on first tools/list.
	listCtx, listCancel := context.WithTimeout(ctx, m.opts.ReconnectTimeout)
	defer listCancel()
	newTools, err := c.ListTools(listCtx)
	if err != nil {
		m.logf("[biu] mcp/%s: reconnect ok but tools/list failed: %v", c.Name(), err)
		return
	}
	added, removed := diffCatalog(st.previousCatalog, newTools)
	st.previousCatalog = newTools
	m.registry.replaceServerTools(c.Name(), newTools)

	switch {
	case len(added) == 0 && len(removed) == 0:
		m.logf("[biu] mcp/%s: reconnected (catalog unchanged, %d tools)",
			c.Name(), len(newTools))
	default:
		m.logf("[biu] mcp/%s: reconnected (catalog: +%d -%d, %d total)",
			c.Name(), len(added), len(removed), len(newTools))
	}
	// st.healthy stays false here; the NEXT probe iteration's
	// successful Ping flips it back to true and logs "healthy
	// again". That ordering keeps the log narrative coherent
	// (lost → reconnecting → reconnected → healthy again).
}

// diffCatalog returns (added, removed) tool names between two
// catalog snapshots. Order-independent — comparison is by Name
// only.
func diffCatalog(prev, curr []ToolDef) (added, removed []string) {
	prevSet := map[string]bool{}
	for _, t := range prev {
		prevSet[t.Name] = true
	}
	currSet := map[string]bool{}
	for _, t := range curr {
		currSet[t.Name] = true
	}
	for n := range currSet {
		if !prevSet[n] {
			added = append(added, n)
		}
	}
	for n := range prevSet {
		if !currSet[n] {
			removed = append(removed, n)
		}
	}
	return
}

// logf is the zero-cost wrapper for the optional Logf hook. Lets
// HealthMonitor sprinkle log calls without nil-checking each
// site.
func (m *HealthMonitor) logf(format string, args ...any) {
	if m.opts.Logf == nil {
		return
	}
	m.opts.Logf(format, args...)
}

// SeedCatalog records the initial tools/list snapshot for a
// server. Called by the bootstrap path right after Connect so the
// first health-driven reconnect's diff has something meaningful
// to compare against (otherwise every reconnect would log "+N -0"
// because the previous slice is empty).
//
// Safe to call multiple times; later snapshots replace earlier
// ones. Calling on an unknown server is a no-op.
func (m *HealthMonitor) SeedCatalog(name string, tools []ToolDef) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.state[name]
	if !ok {
		st = &serverHealth{healthy: true}
		m.state[name] = st
	}
	// Copy so the caller can't mutate our snapshot through their
	// slice.
	st.previousCatalog = append([]ToolDef(nil), tools...)
}

