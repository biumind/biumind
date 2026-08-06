// Package state holds the AppState — the mutable, shared, thread-safe
// container for everything that survives across turns within a
// single QueryEngine session.
//
// It owns:
//
//   * Messages       — full conversation buffer
//   * Tasks          — Task tool registry (running / completed bg jobs)
//   * Files          — read-side cache so the model isn't re-reading
//   * Queries        — chained query metadata (parent/child for sub-agents)
//   * Permissions    — runtime permission grants accumulated this session
//
// Locking strategy: a single RWMutex guards the whole state. Read-heavy
// callers (REPL re-render, tool description() builders) take the read
// lock; mutations go through Update(fn). This is intentionally
// coarse-grained — fine-grained locks per field are not worth the
// complexity at our scale (a chat session has thousands of events,
// not millions).

package state

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// AppState is the in-memory container. Persistence (JSONL session
// log, project-scoped memory under ~/.biumind/projects/...) is
// handled outside.
type AppState struct {
	mu sync.RWMutex

	// SessionID identifies this conversation. Used by hooks and
	// session log to scope events.
	SessionID string

	// Messages is the full conversation buffer. Includes user / assistant
	// / tool_result / system messages. Index is stable: tools may
	// reference messages by index in their results.
	Messages []Message

	// Tasks tracks background jobs (TaskCreate/Get/Stop tools).
	Tasks map[TaskID]*Task

	// Todos is the in-session todo checklist keyed by agentID (empty
	// string for the main agent).
	Todos map[string][]TodoItem

	// Files caches what the model has read. When the model issues
	// a Read tool call, we record the result here; subsequent Reads of
	// the same path within the session can be deduplicated.
	Files map[string]FileState

	// Queries tracks parent/child query chains (sub-agent spawning).
	// Top-level user query has no parent.
	Queries []QueryChain

	// PermissionsGranted: tools the user has approved during this
	// session. Resets at session end. Persistent grants live in
	// settings.json.
	PermissionsGranted map[string]bool

	// Cost accumulates token usage + USD cost across all turns.
	Cost CostSnapshot

	// CreatedAt is when the QueryEngine was instantiated.
	CreatedAt time.Time

	// OriginalCwd is the working directory the engine was launched
	// in, resolved once at startup. Stored on
	// AppState (not derived from os.Getwd() at access time) because
	// Bash tool invocations can `cd` mid-session and we still need
	// the original anchor for permission checks, sandbox allowWrite,
	// and cross-dir CLAUDE.md scanning.
	//
	// Set by wiring at construction; treated as read-only afterward.
	// The empty string means "not yet wired" — callers should fall
	// back to os.Getwd() in that case.
	OriginalCwd string
}

// Message is the canonical conversation entry. We keep a single
// struct and use Role + Content shape to distinguish variants.
type Message struct {
	ID        string         // ULID-ish, deterministic from index for replay
	Role      MessageRole    // user | assistant | tool_result | system
	Content   []ContentBlock // text + tool_use + tool_result + image
	CreatedAt time.Time

	// Assistant-only metadata
	StopReason  string // end_turn | tool_use | max_tokens | stop_sequence
	Model       string
	UsageInput  int
	UsageOutput int

	// Tool-result-only metadata
	ToolUseID string // links tool_result back to the tool_use block
	IsError   bool

	// Set when this message was injected by a tool's `newMessages`.
	InjectedByTool string
}

// MessageRole — string typedef so JSON encoding stays clean.
type MessageRole string

const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleSystem     MessageRole = "system"
	RoleToolResult MessageRole = "tool_result"
)

// ContentBlock is one piece of message content. Discriminated by Type.
//
// Anthropic's Messages API uses a similar shape, so this maps almost
// 1:1 onto the wire format. OpenAI-compat path translates at the
// adapter layer.
type ContentBlock struct {
	Type ContentType `json:"type"`

	// Type=text
	Text string `json:"text,omitempty"`

	// Type=tool_use (assistant requesting a tool call)
	ToolUseID    string         `json:"id,omitempty"`
	ToolUseName  string         `json:"name,omitempty"`
	ToolUseInput map[string]any `json:"input,omitempty"`

	// Type=tool_result (response we feed back to the LLM)
	ToolResultID      string         `json:"tool_use_id,omitempty"`
	ToolResultContent []ContentBlock `json:"content,omitempty"` // may be nested
	ToolResultIsError bool           `json:"is_error,omitempty"`

	// Type=image
	ImageMimeType string `json:"image_mime,omitempty"`
	ImageData     string `json:"image_data,omitempty"` // base64
}

type ContentType string

const (
	ContentText       ContentType = "text"
	ContentToolUse    ContentType = "tool_use"
	ContentToolResult ContentType = "tool_result"
	ContentImage      ContentType = "image"
)

// TaskID is a typed ULID-ish string for tasks.
type TaskID string

// Task is the background-job record plus a couple of forward-looking fields.
type Task struct {
	ID          TaskID
	Type        string // local_bash | local_agent | remote_agent | ...
	Status      string // pending | running | completed | failed | killed
	Description string
	ToolUseID   string

	StartTime time.Time
	EndTime   time.Time

	// OutputFile is the on-disk JSONL or transcript stream. OutputOffset
	// lets a long-running TaskOutput call resume from the last seen
	// position.
	OutputFile   string
	OutputOffset int64

	// Notified=true when the user has been told the task finished
	// (via push notification or in-app banner). Prevents double-notify.
	Notified bool

	// Blocks/BlockedBy form the task dependency graph for the workbench.
	Blocks    []TaskID
	BlockedBy []TaskID
}

// TodoStatus is the lifecycle state of a TodoItem.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// TodoItem carries content + status + activeForm (used by the spinner)
// + a stable ID so updates can identify items across calls without
// relying on slice index.
type TodoItem struct {
	ID         string     `json:"id,omitempty"`
	Content    string     `json:"content"`
	ActiveForm string     `json:"activeForm,omitempty"`
	Status     TodoStatus `json:"status"`
}

// FileState is the cached snapshot of what the model has read. Tools
// like Edit use this to diff against — if the cached version doesn't
// match the on-disk version, Edit refuses (prevents stale edits).
type FileState struct {
	Path     string
	Content  string
	ReadAt   time.Time
	NumLines int
	// SHA-256 of content; cheap way to check "did the file change since
	// we last read it" in Edit tool's freshness check.
	Sha256 string
}

// QueryChain links a sub-agent run to its parent.
type QueryChain struct {
	QueryID  string // ULID for this query
	ParentID string // empty for top-level
	AgentID  string // empty for main session
}

// CostSnapshot is the running tally for this session.
type CostSnapshot struct {
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCacheTokens  int
	TotalUSDMicros    int64 // 0.04 USD = 40000 micros
}

// New creates an empty AppState with a fresh session id.
func New() *AppState {
	return &AppState{
		SessionID:          uuid.New().String(),
		Messages:           []Message{},
		Tasks:              map[TaskID]*Task{},
		Todos:              map[string][]TodoItem{},
		Files:              map[string]FileState{},
		Queries:            []QueryChain{},
		PermissionsGranted: map[string]bool{},
		CreatedAt:          time.Now().UTC(),
	}
}

// ─── Read accessors (RLock) ────────────────────────────

// Snapshot returns a shallow copy of the messages slice. Safe to read
// outside the lock — but the underlying ContentBlock pointers are
// shared. Callers must not mutate. For deep copies, use SnapshotDeep.
func (a *AppState) Snapshot() []Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Message, len(a.Messages))
	copy(out, a.Messages)
	return out
}

// MessageAt returns the message at the given index (stable). Returns
// nil + false on out-of-range.
func (a *AppState) MessageAt(i int) (Message, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if i < 0 || i >= len(a.Messages) {
		return Message{}, false
	}
	return a.Messages[i], true
}

// LastAssistant returns the last assistant message (most recent), or
// false when none.
func (a *AppState) LastAssistant() (Message, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for i := len(a.Messages) - 1; i >= 0; i-- {
		if a.Messages[i].Role == RoleAssistant {
			return a.Messages[i], true
		}
	}
	return Message{}, false
}

// FileSnapshot returns the cached file state for path, if any.
func (a *AppState) FileSnapshot(path string) (FileState, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	fs, ok := a.Files[path]
	return fs, ok
}

// CostNow returns the current cost snapshot (value copy, safe).
func (a *AppState) CostNow() CostSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Cost
}

// ─── Mutators (Lock via Update) ────────────────────────

// Update runs fn under the write lock. Single-callback API keeps
// every mutation in one obvious place.
func (a *AppState) Update(fn func(*AppState)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(a)
}

// AppendMessage is the most common mutation; convenience for
// callers that don't want to write Update(...) by hand.
func (a *AppState) AppendMessage(m Message) {
	a.Update(func(s *AppState) {
		if m.ID == "" {
			m.ID = uuid.New().String()
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = time.Now().UTC()
		}
		s.Messages = append(s.Messages, m)
	})
}

// PutFile records the snapshot of a file the model just read.
func (a *AppState) PutFile(fs FileState) {
	a.Update(func(s *AppState) {
		s.Files[fs.Path] = fs
	})
}

// TrackedFiles returns the paths of every file currently in the
// freshness ledger. Order is the map iteration order — callers
// that need stable ordering must sort. Used by post-compact
// attachment generation to know which files to re-inject.
func (a *AppState) TrackedFiles() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.Files))
	for p := range a.Files {
		out = append(out, p)
	}
	return out
}

// ClearFiles drops every file snapshot. Called after macro compact
// because the message history that referenced those Read results is
// now a summary — the model's belief about which files it has fresh
// reads of no longer aligns with the cached SHA-256 ledger. Forcing
// re-Read is safer than letting Edit operate on what the model
// "remembers" reading before compact.
func (a *AppState) ClearFiles() {
	a.Update(func(s *AppState) {
		s.Files = map[string]FileState{}
	})
}

// AddCost accumulates token + USD usage from a single API call.
func (a *AppState) AddCost(in, out, cache int, usdMicros int64) {
	a.Update(func(s *AppState) {
		s.Cost.TotalInputTokens += in
		s.Cost.TotalOutputTokens += out
		s.Cost.TotalCacheTokens += cache
		s.Cost.TotalUSDMicros += usdMicros
	})
}

// PutTask creates or replaces a task entry.
func (a *AppState) PutTask(t *Task) {
	a.Update(func(s *AppState) {
		if t.ID == "" {
			t.ID = TaskID(uuid.New().String())
		}
		s.Tasks[t.ID] = t
	})
}

// UpdateTask runs fn on the task; no-op if the task doesn't exist.
func (a *AppState) UpdateTask(id TaskID, fn func(*Task)) bool {
	var found bool
	a.Update(func(s *AppState) {
		t, ok := s.Tasks[id]
		if !ok {
			return
		}
		fn(t)
		found = true
	})
	return found
}

// GrantPermission records a session-scoped permission grant.
func (a *AppState) GrantPermission(key string) {
	a.Update(func(s *AppState) {
		s.PermissionsGranted[key] = true
	})
}

// HasPermission checks the session-scoped grant cache.
func (a *AppState) HasPermission(key string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.PermissionsGranted[key]
}

// ResetMessages replaces the message buffer. Used by compact when it
// substitutes a summary for the old history.
func (a *AppState) ResetMessages(next []Message) {
	a.Update(func(s *AppState) {
		s.Messages = next
	})
}

// SetTodos replaces the todo list for the given agent (empty string =
// main agent). When every item is completed the list is cleared, so
// the spinner doesn't keep showing a "0/N" forever.
func (a *AppState) SetTodos(agentID string, items []TodoItem) []TodoItem {
	var prev []TodoItem
	a.Update(func(s *AppState) {
		prev = append([]TodoItem(nil), s.Todos[agentID]...)
		allDone := len(items) > 0
		for _, it := range items {
			if it.Status != TodoCompleted {
				allDone = false
				break
			}
		}
		if allDone {
			delete(s.Todos, agentID)
		} else {
			s.Todos[agentID] = append([]TodoItem(nil), items...)
		}
	})
	return prev
}

// Todos returns a stable copy of the todo list for the agent.
func (a *AppState) TodosFor(agentID string) []TodoItem {
	a.mu.RLock()
	defer a.mu.RUnlock()
	src := a.Todos[agentID]
	out := make([]TodoItem, len(src))
	copy(out, src)
	return out
}

// TasksSnapshot returns a stable view of the running task list.
// Sorted by ID so callers can rely on order without re-sorting.
func (a *AppState) TasksSnapshot() []*Task {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Task, 0, len(a.Tasks))
	for _, t := range a.Tasks {
		copy := *t
		out = append(out, &copy)
	}
	return out
}
