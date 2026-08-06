// File-snapshot store for --rewind-files (P20.57).
//
// Snapshot lifecycle:
//
//   - Before a file-mutating tool (Edit / Write / MultiEdit /
//     NotebookEdit) commits a change, the engine captures a
//     snapshot of the file's pre-edit content keyed by the
//     CURRENT user message's UUID. Multiple edits within one user
//     turn share the same key — only the FIRST snapshot of each
//     path under that key is retained (the user-visible "before
//     this turn" state).
//   - `biu --rewind-files <uuid>` reads every snapshot tagged with
//     <uuid> and restores the corresponding files to their pre-turn
//     content. Snapshots strictly newer than <uuid> are also rolled
//     back so the filesystem matches the state right before the
//     user's <uuid> message was sent.
//
// Storage layout:
//
//   ~/.biumind/snapshots/<session-id>/
//     index.jsonl              -- one row per snapshot (UUID, path, ref)
//     blobs/<sha256>           -- content-addressed blob (deduped)
//
// Identical content across edits dedupes naturally — we never store
// the same body twice. The index is append-only so concurrent writes
// don't corrupt each other; rewind reads it linearly.

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SnapshotStore captures pre-edit file content keyed by user-message
// UUID + abs path. Process-scoped; safe for concurrent use.
type SnapshotStore struct {
	mu       sync.Mutex
	root     string
	indexPath string

	// seen prevents double-snapshotting the same (uuid, path) within
	// one process — the FIRST snapshot is the user-visible "before
	// this turn" state.
	seen map[string]bool
}

// SnapshotIndexEntry is one row in index.jsonl.
type SnapshotIndexEntry struct {
	UUID    string    `json:"uuid"`     // user message UUID this snapshot belongs to
	Path    string    `json:"path"`     // absolute path snapshotted
	BlobRef string    `json:"blob_ref"` // sha256 of the content (filename in blobs/)
	Size    int64     `json:"size"`
	TS      time.Time `json:"ts"`
	// Existed=false means "the file didn't exist at snapshot time" —
	// rewind should DELETE the file rather than restore it.
	Existed bool `json:"existed"`
}

// NewSnapshotStore opens (creates) a snapshot store under
// ~/.biumind/snapshots/<sessionID>/. Returns an error only on
// filesystem trouble; missing dir is not an error.
func NewSnapshotStore(home, sessionID string) (*SnapshotStore, error) {
	if home == "" || sessionID == "" {
		return nil, errors.New("snapshots: home and sessionID required")
	}
	root := filepath.Join(home, ".biumind", "snapshots", sessionID)
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o755); err != nil {
		return nil, fmt.Errorf("snapshots: mkdir: %w", err)
	}
	return &SnapshotStore{
		root:      root,
		indexPath: filepath.Join(root, "index.jsonl"),
		seen:      map[string]bool{},
	}, nil
}

// Capture snapshots `path`'s pre-edit content under `uuid`. Idempotent
// per (uuid, path) — repeated calls with the same key are no-ops.
// path missing on disk is recorded as Existed=false so rewind knows
// to delete a created-then-rewound file.
func (s *SnapshotStore) Capture(uuid, path string) error {
	if s == nil {
		return nil
	}
	if uuid == "" || path == "" {
		return errors.New("snapshots: uuid and path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("snapshots: abs %s: %w", path, err)
	}
	key := uuid + "\x00" + abs

	s.mu.Lock()
	if s.seen[key] {
		s.mu.Unlock()
		return nil
	}
	s.seen[key] = true
	s.mu.Unlock()

	entry := SnapshotIndexEntry{
		UUID: uuid, Path: abs, TS: time.Now().UTC(),
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			entry.Existed = false
			return s.appendIndex(entry)
		}
		return fmt.Errorf("snapshots: read %s: %w", abs, err)
	}
	entry.Existed = true
	entry.Size = int64(len(data))

	sum := sha256.Sum256(data)
	entry.BlobRef = hex.EncodeToString(sum[:])
	blobPath := filepath.Join(s.root, "blobs", entry.BlobRef)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		// Atomic write so a partial blob on crash doesn't poison
		// future restores.
		tmp := blobPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return fmt.Errorf("snapshots: write blob: %w", err)
		}
		if err := os.Rename(tmp, blobPath); err != nil {
			return fmt.Errorf("snapshots: rename blob: %w", err)
		}
	}
	return s.appendIndex(entry)
}

func (s *SnapshotStore) appendIndex(e SnapshotIndexEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.indexPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("snapshots: open index: %w", err)
	}
	defer f.Close()
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		return err
	}
	return nil
}

// Entries returns every snapshot row in disk order (oldest first).
// Used by rewind to walk back through history.
func (s *SnapshotStore) Entries() ([]SnapshotIndexEntry, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SnapshotIndexEntry
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e SnapshotIndexEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // tolerate corrupt rows
		}
		out = append(out, e)
	}
	return out, nil
}

// HasUUID reports whether the index has at least one entry for
// `uuid` — used by rewind to validate the user-supplied id before
// touching the filesystem.
func (s *SnapshotStore) HasUUID(uuid string) (bool, error) {
	entries, err := s.Entries()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.UUID == uuid {
			return true, nil
		}
	}
	return false, nil
}

// Rewind restores the filesystem to its state immediately BEFORE the
// `targetUUID` message was sent. Algorithm:
//
//  1. Walk all entries from oldest to newest. The set of UUIDs in
//     index order tells us which messages have snapshots.
//  2. Find targetUUID's first occurrence — call its index position N.
//  3. For each path that appears in entries[N..], restore its FIRST
//     occurrence (= the pre-state of whichever message first
//     touched it from N onwards). Files marked Existed=false are
//     deleted instead.
//  4. Paths touched only BEFORE N are left untouched (those edits
//     are part of older history we're not rewinding).
//
// Returns the number of files restored + a list of the paths.
func (s *SnapshotStore) Rewind(targetUUID string) (int, []string, error) {
	entries, err := s.Entries()
	if err != nil {
		return 0, nil, err
	}
	startIdx := -1
	for i, e := range entries {
		if e.UUID == targetUUID {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return 0, nil, fmt.Errorf("snapshots: no entries for uuid %q", targetUUID)
	}

	// Per-path: keep the FIRST snapshot at-or-after startIdx (that's
	// the pre-edit state we want to restore).
	firstByPath := map[string]SnapshotIndexEntry{}
	pathOrder := []string{}
	for i := startIdx; i < len(entries); i++ {
		e := entries[i]
		if _, ok := firstByPath[e.Path]; ok {
			continue
		}
		firstByPath[e.Path] = e
		pathOrder = append(pathOrder, e.Path)
	}

	restored := []string{}
	for _, p := range pathOrder {
		e := firstByPath[p]
		if !e.Existed {
			if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
				return len(restored), restored,
					fmt.Errorf("snapshots: remove %s: %w", e.Path, err)
			}
			restored = append(restored, e.Path)
			continue
		}
		blob := filepath.Join(s.root, "blobs", e.BlobRef)
		data, err := os.ReadFile(blob)
		if err != nil {
			return len(restored), restored,
				fmt.Errorf("snapshots: read blob %s: %w", e.BlobRef, err)
		}
		// Re-create parent dir in case the original directory was
		// deleted by an intervening edit.
		if err := os.MkdirAll(filepath.Dir(e.Path), 0o755); err != nil {
			return len(restored), restored,
				fmt.Errorf("snapshots: mkdir parent: %w", err)
		}
		if err := os.WriteFile(e.Path, data, 0o644); err != nil {
			return len(restored), restored,
				fmt.Errorf("snapshots: restore %s: %w", e.Path, err)
		}
		restored = append(restored, e.Path)
	}
	return len(restored), restored, nil
}
