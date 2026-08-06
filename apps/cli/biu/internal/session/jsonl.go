// Package session writes JSONL session logs compatible with Claude Code's format.
//
// One file per session: ~/.biu/sessions/<project-hash>/<session-id>.jsonl
// Each line is a JSON event:
//
//	{"type":"user_message","ts":"...","content":"..."}
//	{"type":"assistant_delta","ts":"...","content":"..."}
//	{"type":"assistant_message","ts":"...","content":"..."}
//	{"type":"tool_use","ts":"...","name":"read","args":{...},"call_id":"..."}
//	{"type":"tool_result","ts":"...","call_id":"...","output":"..."}
//	{"type":"end","ts":"...","reason":"end_turn"}
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	Type    string         `json:"type"`
	TS      time.Time      `json:"ts"`
	Content string         `json:"content,omitempty"`
	Name    string         `json:"name,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	CallID  string         `json:"call_id,omitempty"`
	Output  string         `json:"output,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	// UUID is the message-level identifier added by P20.57 so
	// --rewind-files can target a specific user message. Populated
	// for user_message events; absent on tool-use rows. Old session
	// files without this field still load — Replay treats missing
	// UUIDs as opaque and `--rewind-files` simply can't address them.
	UUID string `json:"uuid,omitempty"`
}

type Writer struct {
	mu sync.Mutex
	f  *os.File
	id string
}

// Open creates a new session file and returns a Writer.
// dir is typically ~/.biu/sessions; we create dir/<project-hash>/<sid>.jsonl.
func Open(dir, projectHash string) (*Writer, error) {
	if projectHash == "" {
		projectHash = "default"
	}
	parent := filepath.Join(dir, projectHash)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	sid := newID()
	path := filepath.Join(parent, sid+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, id: sid}, nil
}

func (w *Writer) ID() string   { return w.id }
func (w *Writer) Path() string { return w.f.Name() }

// Append writes an event to the JSONL log. Side effect (P20.57):
// when type is "user_message" and UUID is empty, a fresh UUID is
// generated and assigned to the event before write. The auto-assigned
// id is NOT returned through the existing signature (callers don't
// need it for the legacy path); use AppendWithUUID when the caller
// wants the assigned id back (e.g. file-snapshot capture).
func (w *Writer) Append(e Event) error {
	_, err := w.AppendWithUUID(e)
	return err
}

// AppendWithUUID is the UUID-returning variant. The assigned UUID
// is reported even when one was supplied (so callers can chain).
// Empty Writer or non-user_message events return "" + nil.
func (w *Writer) AppendWithUUID(e Event) (string, error) {
	if w == nil || w.f == nil {
		return "", nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if e.UUID == "" && e.Type == "user_message" {
		e.UUID = newUUID()
	}
	body, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	if _, err := w.f.Write(body); err != nil {
		return "", err
	}
	if _, err := w.f.Write([]byte{'\n'}); err != nil {
		return "", err
	}
	return e.UUID, nil
}

func (w *Writer) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}

func newID() string {
	now := time.Now().UTC().Format("20060102-150405")
	r := make([]byte, 4)
	_, _ = rand.Read(r)
	return fmt.Sprintf("%s-%s", now, hex.EncodeToString(r))
}

// newUUID generates an opaque short id for per-message addressing
// (P20.57). Uses crypto/rand for collision resistance; 16 bytes hex
// = 32 chars. Format-only difference from session ids — separate fn
// so a future "real RFC 4122 UUID" swap doesn't have to bump the
// session-id format.
func newUUID() string {
	r := make([]byte, 16)
	_, _ = rand.Read(r)
	return hex.EncodeToString(r)
}
