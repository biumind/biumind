// Reader / Index helpers for biu session JSONL files.
//
// The Writer above logs every turn — these helpers let us list past
// sessions, inspect one, and replay its events back into a fresh
// AppState so `biu --resume <id>` can pick up exactly where the
// previous run left off.
//
// Filesystem layout (matches the Writer):
//
//   ~/.biu/sessions/<project-hash>/<session-id>.jsonl
//
// We don't store a separate `meta.json` today — every fact about a
// session is reconstructable from its event stream + filesystem
// metadata (mtime, size).

package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// Summary is the shape returned by ListSessions: just enough to render
// a `biu sessions list` table without loading every event.
type Summary struct {
	ID           string
	ProjectHash  string
	Path         string
	BytesOnDisk  int64
	FirstPrompt  string // truncated to ~100 chars
	MessageCount int
}

// ListSessions returns every JSONL file under dir, sorted newest
// first. dir is typically ~/.biu/sessions. Missing dir → empty
// result, no error.
func ListSessions(dir string) ([]Summary, error) {
	out := []Summary{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, projEntry := range entries {
		if !projEntry.IsDir() {
			continue
		}
		projDir := filepath.Join(dir, projEntry.Name())
		files, _ := os.ReadDir(projDir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			fullPath := filepath.Join(projDir, f.Name())
			info, err := f.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".jsonl")
			s := Summary{
				ID:          id,
				ProjectHash: projEntry.Name(),
				Path:        fullPath,
				BytesOnDisk: info.Size(),
			}
			peek(&s)
			out = append(out, s)
		}
	}
	// Newest first by ID (writer's ID encodes timestamp + random).
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// peek reads the first user_message + counts events to populate
// Summary fields. Failures leave the fields blank — list-style UIs
// don't need to fail wholesale on one corrupt file.
func peek(s *Summary) {
	f, err := os.Open(s.Path)
	if err != nil {
		return
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		s.MessageCount++
		if s.FirstPrompt != "" {
			continue
		}
		var e Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			continue
		}
		if e.Type == "user_message" {
			s.FirstPrompt = truncate(e.Content, 100)
		}
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// FindByID returns a Summary for the given session id (without
// .jsonl). Searches every project subdirectory. Returns ok=false
// when not found.
func FindByID(dir, id string) (Summary, bool) {
	all, err := ListSessions(dir)
	if err != nil {
		return Summary{}, false
	}
	for _, s := range all {
		if s.ID == id {
			return s, true
		}
	}
	return Summary{}, false
}

// FindByIndex returns the n-th most recent session (1-based). Used by
// the `/resume #<n>` shorthand so users can pick from the inline
// session list without typing the full ID. Returns ok=false when n
// is out of range or the directory is empty.
func FindByIndex(dir string, n int) (Summary, bool) {
	if n <= 0 {
		return Summary{}, false
	}
	all, err := ListSessions(dir)
	if err != nil || n > len(all) {
		return Summary{}, false
	}
	return all[n-1], true
}

// FindLatest returns the most-recently-modified session in dir, or
// ok=false when the directory has none. Convenience wrapper around
// FindByIndex(dir, 1) so callers can spell intent ("latest") rather
// than a magic number.
func FindLatest(dir string) (Summary, bool) {
	return FindByIndex(dir, 1)
}

// Replay reads `path`, parses every event, and rebuilds an AppState's
// message slice from user_message + assistant_message events. Tool
// use/result events are folded back as ContentToolUse / ContentToolResult
// blocks so the model sees the same context it had at the time.
//
// This is the primitive the `--resume <id>` and `/resume` slash
// command build on.
func Replay(path string, st *state.AppState) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var msgs []state.Message
	pendingAssistant := state.Message{Role: state.RoleAssistant}
	flushAssistant := func() {
		if len(pendingAssistant.Content) > 0 {
			msgs = append(msgs, pendingAssistant)
			pendingAssistant = state.Message{Role: state.RoleAssistant}
		}
	}
	for scan.Scan() {
		var e Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			continue
		}
		switch e.Type {
		case "user_message":
			flushAssistant()
			msgs = append(msgs, state.Message{
				Role: state.RoleUser,
				Content: []state.ContentBlock{{
					Type: state.ContentText, Text: e.Content,
				}},
			})
		case "assistant_message":
			flushAssistant()
			msgs = append(msgs, state.Message{
				Role: state.RoleAssistant,
				Content: []state.ContentBlock{{
					Type: state.ContentText, Text: e.Content,
				}},
			})
		case "tool_use":
			pendingAssistant.Content = append(pendingAssistant.Content,
				state.ContentBlock{
					Type:        state.ContentToolUse,
					ToolUseID:   e.CallID,
					ToolUseName: e.Name,
					ToolUseInput: e.Args,
				})
		case "tool_result":
			flushAssistant()
			msgs = append(msgs, state.Message{
				Role: state.RoleUser,
				Content: []state.ContentBlock{{
					Type:              state.ContentToolResult,
					ToolResultID:      e.CallID,
					ToolResultContent: []state.ContentBlock{{Type: state.ContentText, Text: e.Output}},
				}},
			})
		}
	}
	flushAssistant()
	st.ResetMessages(msgs)
	return scan.Err()
}

// SessionsDir returns the canonical sessions dir under $HOME/.biu
// (creating it on demand). Mirrors config.SessionsDir() but lives
// here so reader-side callers don't pull the config package.
func SessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".biu", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
