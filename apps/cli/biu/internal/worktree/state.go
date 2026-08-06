// Package worktree persists "currently-active worktree" info across
// biu process restarts.
//
// Each session that calls EnterWorktree drops a JSON sidecar at
// ~/.biu/sessions/<project>/<session-id>.worktree.json. On
// `biu --resume <id>` the launcher reads the sidecar, verifies the
// worktree still exists on disk, and bumps the engine's cwd.
//
// File shape:
//
//   {
//     "session_id": "20260525-103045-abcdef",
//     "previous":   "/abs/path/to/main/repo",
//     "current":    "/abs/path/to/main/repo/.biumind/worktrees/feat-x",
//     "branch":     "biu/feat-x",
//     "created_at": "2026-05-25T10:30:45Z"
//   }
//
// We deliberately keep this separate from the session JSONL so a
// stale worktree (deleted directory after biu crashed) is one
// `rm <id>.worktree.json` away from clean — no need to rewrite the
// whole event log.

package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is the persisted snapshot.
type State struct {
	SessionID string    `json:"session_id"`
	Previous  string    `json:"previous"`
	Current   string    `json:"current"`
	Branch    string    `json:"branch,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store reads/writes worktree sidecar files. Concurrency-safe via
// atomic file replace; the in-memory map is mutex-guarded for tests.
type Store struct {
	root string // base sessions dir (~/.biu/sessions by default)
}

// NewStore returns a store rooted at the supplied dir. Empty dir
// resolves to the default ~/.biu/sessions location.
func NewStore(root string) (*Store, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".biu", "sessions")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// pathFor resolves the sidecar path for a session id. We don't know
// the project hash up front, so the file lives under
// `<root>/_worktrees/<session-id>.json`. Co-locating under the
// project subdir would require coordination with session.Open's
// hash, and the savings (one fewer file lookup) aren't worth the
// coupling.
func (s *Store) pathFor(sessionID string) string {
	return filepath.Join(s.root, "_worktrees", sessionID+".json")
}

// Save writes the state, atomically replacing any prior file.
func (s *Store) Save(st State) error {
	if st.SessionID == "" {
		return errors.New("worktree: SessionID required")
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now().UTC()
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	p := s.pathFor(st.SessionID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads the sidecar for a session id. Missing file → (zero, nil)
// so callers can branch on `state.Current == ""`.
func (s *Store) Load(sessionID string) (State, error) {
	body, err := os.ReadFile(s.pathFor(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return State{}, fmt.Errorf("worktree: parse %s: %w",
			s.pathFor(sessionID), err)
	}
	return st, nil
}

// Delete removes the sidecar. Missing file is not an error.
func (s *Store) Delete(sessionID string) error {
	err := os.Remove(s.pathFor(sessionID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// VerifyAndResume returns the State only when its `current` directory
// still exists. Stale entries (worktree manually rm'd) get cleaned up
// here so subsequent `biu sessions list` doesn't show ghosts.
func (s *Store) VerifyAndResume(sessionID string) (State, bool, error) {
	st, err := s.Load(sessionID)
	if err != nil {
		return State{}, false, err
	}
	if st.Current == "" {
		return State{}, false, nil
	}
	info, err := os.Stat(st.Current)
	if err != nil || !info.IsDir() {
		_ = s.Delete(sessionID) // cleanup
		return State{}, false, nil
	}
	return st, true, nil
}

// List returns every active worktree state. Used by `biu sessions
// list --worktrees` and the `/agents` slash command's status
// summary. Stale entries are skipped (and removed).
func (s *Store) List() ([]State, error) {
	dir := filepath.Join(s.root, "_worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []State
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		st, ok, _ := s.VerifyAndResume(id)
		if !ok {
			continue
		}
		out = append(out, st)
	}
	return out, nil
}
