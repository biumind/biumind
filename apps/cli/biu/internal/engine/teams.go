// Team metadata layer for the async-agent swarm (P20.53-2). Sits on
// top of AsyncAgentStore: the store handles fire-and-forget execution;
// Teams provide named groups + human-friendly addressing.
//
// Scope-reduced for biu's single-process model:
//
//   - Teams live in process memory only (no ~/.claude/teams/ JSON files).
//     A future commit can add disk persistence for `--continue` to
//     reattach to a still-running team across REPL restarts; today the
//     team graph is per-session.
//   - Each teammate has a friendly Name (e.g. "researcher") in addition
//     to the handle ID (e.g. "agent-3"). Names are scoped per-team so
//     two teams can both have a "lead". Resolution: lookup goes
//     name → handle, then handle → store entry.
//   - SendMessage queues a follow-up prompt addressed by name. Delivery
//     model: the queue is appended to the teammate's PendingMessages
//     slice; when the teammate's current Submit returns, the engine
//     pulls one queued message off the front and submits it. Effectively
//     "interactive" but without mid-Submit interruption (which would
//     require teammate-side ctx cancellation surgery).

package engine

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// TeamRegistry holds the named-team graph for one engine session.
// Process-scoped; nil-safe.
type TeamRegistry struct {
	mu    sync.RWMutex
	teams map[string]*Team // by team name
}

// Team is one named group of teammates.
type Team struct {
	Name        string
	Description string
	// Members maps friendly name → teammate handle id. The handle
	// itself lives in the AsyncAgentStore (Active or Pending).
	Members map[string]string
}

// NewTeamRegistry returns an empty registry.
func NewTeamRegistry() *TeamRegistry {
	return &TeamRegistry{teams: map[string]*Team{}}
}

// Create registers a new empty team. Returns an error if a team with
// that name already exists — duplicate-name creation is a model bug
// the tool should surface, not silently merge.
func (r *TeamRegistry) Create(name, description string) (*Team, error) {
	if r == nil {
		return nil, errors.New("team registry not initialised")
	}
	if name == "" {
		return nil, errors.New("team name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.teams[name]; exists {
		return nil, fmt.Errorf("team %q already exists", name)
	}
	t := &Team{Name: name, Description: description, Members: map[string]string{}}
	r.teams[name] = t
	return t, nil
}

// Delete removes a team. Does NOT cancel teammates currently in flight
// (they finish on their own and write to the AsyncAgentStore as usual);
// the deletion just unregisters the addressing layer. Returns the
// removed team for telemetry, or false if the name was unknown.
func (r *TeamRegistry) Delete(name string) (*Team, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.teams[name]
	if !ok {
		return nil, false
	}
	delete(r.teams, name)
	return t, true
}

// Get returns the team by name. ok=false when unknown.
func (r *TeamRegistry) Get(name string) (*Team, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.teams[name]
	if !ok {
		return nil, false
	}
	// Return a snapshot copy so callers can iterate Members without
	// holding our lock. The struct itself is small.
	cp := *t
	cp.Members = make(map[string]string, len(t.Members))
	for k, v := range t.Members {
		cp.Members[k] = v
	}
	return &cp, true
}

// AddMember associates a friendly name with a handle in a team. The
// caller is responsible for actually spawning the teammate (typically
// via SpawnAsync). AddMember just records the addressing.
func (r *TeamRegistry) AddMember(team, memberName, handleID string) error {
	if r == nil {
		return errors.New("team registry not initialised")
	}
	if memberName == "" || handleID == "" {
		return errors.New("memberName and handleID required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.teams[team]
	if !ok {
		return fmt.Errorf("team %q not found", team)
	}
	if _, exists := t.Members[memberName]; exists {
		return fmt.Errorf("member %q already in team %q", memberName, team)
	}
	t.Members[memberName] = handleID
	return nil
}

// ResolveMember finds the handle id for a (team, member-name) pair.
// Returns "" + false on miss.
func (r *TeamRegistry) ResolveMember(team, memberName string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.teams[team]
	if !ok {
		return "", false
	}
	id, ok := t.Members[memberName]
	return id, ok
}

// AllTeams returns a name-sorted snapshot of every registered team.
// Used by the model-facing TeamList variant + diagnostic commands.
func (r *TeamRegistry) AllTeams() []*Team {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Team, 0, len(r.teams))
	for _, t := range r.teams {
		cp := *t
		cp.Members = make(map[string]string, len(t.Members))
		for k, v := range t.Members {
			cp.Members[k] = v
		}
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PendingMessage is one queued follow-up the parent (or another
// teammate) wants delivered to a specific handle on the next Submit.
// Created by SendMessageTool; consumed by SpawnAsync's goroutine when
// it finishes its current Submit.
type PendingMessage struct {
	Body string
	From string // sender's human-friendly label, "user" / "team-lead" / etc.
}

// MessageInbox is the per-teammate follow-up queue. Lives alongside
// the team registry because messages are addressed by team+name; the
// store keys them by handle id for fast lookup.
type MessageInbox struct {
	mu sync.Mutex
	// queues is keyed by teammate handle id ("agent-3") so
	// SendMessage's resolution layer (team+name → handle) is decoupled
	// from delivery. nil ⇒ no queued messages.
	queues map[string][]PendingMessage
}

// NewMessageInbox returns an empty inbox.
func NewMessageInbox() *MessageInbox {
	return &MessageInbox{queues: map[string][]PendingMessage{}}
}

// Enqueue appends a message for the named handle. Idempotent in the
// sense that the same body can be queued twice (the model may send
// repeats; we don't dedupe — it's the model's responsibility not to
// spam). Returns the new queue depth.
func (i *MessageInbox) Enqueue(handleID string, msg PendingMessage) int {
	if i == nil || handleID == "" {
		return 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.queues == nil {
		i.queues = map[string][]PendingMessage{}
	}
	i.queues[handleID] = append(i.queues[handleID], msg)
	return len(i.queues[handleID])
}

// Dequeue removes and returns the next queued message for `handleID`.
// ok=false when the queue is empty.
func (i *MessageInbox) Dequeue(handleID string) (PendingMessage, bool) {
	if i == nil || handleID == "" {
		return PendingMessage{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	q := i.queues[handleID]
	if len(q) == 0 {
		return PendingMessage{}, false
	}
	head := q[0]
	if len(q) == 1 {
		delete(i.queues, handleID)
	} else {
		// Shift left rather than reslice-and-leak: queues are tiny
		// (typically ≤3 messages) so the copy is cheap and the
		// underlying array can be GC'd.
		nq := make([]PendingMessage, len(q)-1)
		copy(nq, q[1:])
		i.queues[handleID] = nq
	}
	return head, true
}

// Depth reports the queue size for a handle without dequeuing.
// Mostly used by tests + diagnostic surfaces.
func (i *MessageInbox) Depth(handleID string) int {
	if i == nil || handleID == "" {
		return 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.queues[handleID])
}
