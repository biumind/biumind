// E2E tests for the --rewind-files file-history restore (P20.57).
//
// Unit tests in session/snapshots_test.go cover the SnapshotStore's
// Capture/Rewind primitives in isolation. This file glues:
//
//   QueryEngine.SetSnapshotCapture →
//   ToolEnv.SnapshotFile callback   →
//   Edit / Write tool's pre-mutation snapshot →
//   SnapshotStore on disk (blobs/ + index.jsonl) →
//   Rewind(uuid) replays
//
// We don't drive cmd/biu/main.go's flag-handling code (that's a small
// orchestration shell over the store; covered in cmd/biu/main_test.go
// equivalents). Instead these tests exercise the engine-level
// integration and prove that:
//
//   - a tool-driven Write captures the pre-state under the right uuid
//   - Rewind restores that state, deletes new files, dedups blobs
//   - the engine doesn't capture when no uuid is set (sub-agent path)
//
// All tests use t.TempDir() for both the snapshot home AND the target
// files so nothing leaks between runs.

package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/files"
)

// ─── helpers ────────────────────────────────────────────────

// rewindHarness builds an engine wired to a SnapshotStore so Edit /
// Write tools land snapshots. Returns the harness so tests can
// inspect post-flight state.
type rewindHarness struct {
	eng     *engine.QueryEngine
	store   *session.SnapshotStore
	st      *state.AppState
	prov    *replayableScript
	homeDir string
}

func newRewindHarness(t *testing.T, prov *replayableScript) *rewindHarness {
	t.Helper()
	home := t.TempDir()
	store, err := session.NewSnapshotStore(home, "rewind-e2e")
	if err != nil {
		t.Fatalf("new snapshot store: %v", err)
	}

	reg := engine.NewRegistry()
	reg.Register(files.WriteTool{})
	reg.Register(files.EditTool{})
	reg.Register(files.ReadTool{})

	st := state.New()
	perms := permissions.NewContext()
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorAllow,
		[]string{"Write", "Edit", "Read"})

	eng, err := engine.New(engine.Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		Permissions: perms, MaxToolTurns: 6,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	eng.SetSnapshotCapture(store.Capture)

	return &rewindHarness{
		eng: eng, store: store, st: st, prov: prov, homeDir: home,
	}
}

// runWithUUID stamps the engine's currentUserUUID before driving one
// Submit. Mirrors what repl/model.go does between user prompts.
func (h *rewindHarness) runWithUUID(t *testing.T, uuid, prompt string) {
	t.Helper()
	h.eng.SetCurrentUserUUID(uuid)
	events := drainAll(h.eng.Submit(context.Background(), prompt))
	if !hasDone(events) {
		t.Fatalf("Submit %q with uuid %q did not reach DoneEvent", prompt, uuid)
	}
}

// replayableScript is a per-turn scripted provider whose script can
// be appended at runtime. Tests build the next turn's script after
// observing tool output (e.g. captured task ids, dynamic paths).
type replayableScript struct {
	mu    sync.Mutex
	turns [][]engine.StreamFrame
	idx   int
}

func newReplayable(turns ...[]engine.StreamFrame) *replayableScript {
	return &replayableScript{turns: turns}
}

func (p *replayableScript) Stream(_ context.Context, _ engine.StreamRequest) (<-chan engine.StreamFrame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.turns) {
		return nil, errors.New("replayableScript: exhausted at " + itoa(p.idx+1))
	}
	frames := p.turns[p.idx]
	p.idx++
	ch := make(chan engine.StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

func (p *replayableScript) appendTurns(turns ...[]engine.StreamFrame) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turns = append(p.turns, turns...)
}

// writeUseTurn — convenience for Write tool_use frames.
func writeUseTurn(useID, path, content string) []engine.StreamFrame {
	return toolUseTurn(useID, "Write",
		`{"file_path":"`+path+`","content":`+jsonString(content)+`}`)
}

// readThenWriteTurns generates the two-turn sequence Edit/Write tools
// require for *existing* files: Read first (to satisfy the freshness
// gate), then Write. Use writeUseTurn directly for new-file writes.
func readThenWriteTurns(rUseID, wUseID, path, content string) [][]engine.StreamFrame {
	return [][]engine.StreamFrame{
		toolUseTurn(rUseID, "Read", `{"file_path":"`+path+`"}`),
		writeUseTurn(wUseID, path, content),
	}
}

// jsonString safely quotes a string for JSON (no funky escapes).
func jsonString(s string) string {
	out := "\""
	for _, r := range s {
		switch r {
		case '"':
			out += "\\\""
		case '\\':
			out += "\\\\"
		case '\n':
			out += "\\n"
		case '\t':
			out += "\\t"
		default:
			out += string(r)
		}
	}
	return out + "\""
}

// readBytes reads a file path or returns empty + nil on missing.
func readBytes(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(data)
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// blobsCount returns the number of blob files under the store's
// blobs/ directory. Used to assert dedup.
func blobsCount(t *testing.T, home string) int {
	t.Helper()
	dir := filepath.Join(home, ".biumind", "snapshots", "rewind-e2e", "blobs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read blobs: %v", err)
	}
	return len(entries)
}

// ─── Case 1: single-edit rewind restores content ───────────

func TestRewindE2E_SingleEdit_RestoresContent(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "code.go")
	mustWrite(t, target, "v1")

	turns := readThenWriteTurns("r1", "w1", target, "v2")
	prov := newReplayable(turns[0], turns[1], textTurn("ack"))
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "edit it")

	if got := readBytes(t, target); got != "v2" {
		t.Fatalf("target should be v2 post-edit; got %q", got)
	}

	if _, _, err := h.store.Rewind("uuid-A"); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if got := readBytes(t, target); got != "v1" {
		t.Errorf("rewind should restore v1; got %q", got)
	}
}

// ─── Case 2: new file (Existed=false) gets deleted on rewind ─

func TestRewindE2E_NewFile_DeletedOnRewind(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "fresh.txt")

	prov := newReplayable(
		writeUseTurn("w1", target, "hello"),
		textTurn("ack"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "create file")

	if got := readBytes(t, target); got != "hello" {
		t.Fatalf("target should exist post-Write; got %q", got)
	}
	if _, _, err := h.store.Rewind("uuid-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("rewind should delete created file; stat err=%v", err)
	}
}

// ─── Case 3: multiple files in one UUID — all restored ─────

func TestRewindE2E_MultiFile_AllRestored(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a.txt")
	b := filepath.Join(tmp, "b.txt")
	mustWrite(t, a, "A1")
	mustWrite(t, b, "B1")

	// Read both first (one turn each), then write both.
	prov := newReplayable(
		toolUseTurn("ra", "Read", `{"file_path":"`+a+`"}`),
		toolUseTurn("rb", "Read", `{"file_path":"`+b+`"}`),
		writeUseTurn("wa", a, "A2"),
		writeUseTurn("wb", b, "B2"),
		textTurn("done"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "edit both")

	if readBytes(t, a) != "A2" || readBytes(t, b) != "B2" {
		t.Fatalf("post-edit state wrong: a=%q b=%q", readBytes(t, a), readBytes(t, b))
	}
	if _, _, err := h.store.Rewind("uuid-A"); err != nil {
		t.Fatal(err)
	}
	if got := readBytes(t, a); got != "A1" {
		t.Errorf("a should rewind to A1; got %q", got)
	}
	if got := readBytes(t, b); got != "B1" {
		t.Errorf("b should rewind to B1; got %q", got)
	}
}

// ─── Case 4: SHA dedup — same pre-content → one blob ───────

func TestRewindE2E_SHADedup_SharedBlob(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a.txt")
	b := filepath.Join(tmp, "b.txt")
	mustWrite(t, a, "SAME")
	mustWrite(t, b, "SAME") // identical pre-content

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+a+`"}`),
		writeUseTurn("w1", a, "AA"),
		textTurn("ack-a"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "p1")

	prov.appendTurns(
		toolUseTurn("r2", "Read", `{"file_path":"`+b+`"}`),
		writeUseTurn("w2", b, "BB"),
		textTurn("ack-b"),
	)
	h.runWithUUID(t, "uuid-B", "p2")

	if n := blobsCount(t, h.homeDir); n != 1 {
		t.Errorf("expected 1 deduped blob, got %d", n)
	}
}

// ─── Case 5: rewind across multiple UUIDs ──────────────────

// Three UUIDs: A→B→C each edits the same file. Rewind to A → file
// should be restored to its pre-A state (the original disk content),
// not to v2 or v3.
func TestRewindE2E_AcrossMultipleUUIDs(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")
	mustWrite(t, target, "v0")

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w1", target, "v1"), textTurn("ack-1"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "p1")

	prov.appendTurns(
		toolUseTurn("r2", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w2", target, "v2"), textTurn("ack-2"))
	h.runWithUUID(t, "uuid-B", "p2")

	prov.appendTurns(
		toolUseTurn("r3", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w3", target, "v3"), textTurn("ack-3"))
	h.runWithUUID(t, "uuid-C", "p3")

	if _, _, err := h.store.Rewind("uuid-A"); err != nil {
		t.Fatal(err)
	}
	if got := readBytes(t, target); got != "v0" {
		t.Errorf("rewind to A should give v0; got %q", got)
	}
}

// ─── Case 6: rewind from a middle UUID ─────────────────────

// Rewind to B should give the post-A / pre-B state, i.e. v1.
func TestRewindE2E_FromMiddleUUID(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")
	mustWrite(t, target, "v0")

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w1", target, "v1"), textTurn("ack-1"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "p1")

	prov.appendTurns(
		toolUseTurn("r2", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w2", target, "v2"), textTurn("ack-2"))
	h.runWithUUID(t, "uuid-B", "p2")

	if _, _, err := h.store.Rewind("uuid-B"); err != nil {
		t.Fatal(err)
	}
	if got := readBytes(t, target); got != "v1" {
		t.Errorf("rewind to B should give pre-B (v1); got %q", got)
	}
}

// ─── Case 7: nonexistent UUID errors cleanly ──────────────

func TestRewindE2E_NonexistentUUID_Errors(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")
	mustWrite(t, target, "v0")

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w1", target, "v1"), textTurn("ack"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "p1")

	if _, _, err := h.store.Rewind("never-existed"); err == nil {
		t.Errorf("rewind to unknown uuid should error")
	}
	// Disk untouched.
	if got := readBytes(t, target); got != "v1" {
		t.Errorf("file should be unchanged after failed rewind; got %q", got)
	}
}

// ─── Case 8: empty store — HasUUID false, Rewind errors ────

func TestRewindE2E_EmptyStore(t *testing.T) {
	home := t.TempDir()
	store, err := session.NewSnapshotStore(home, "empty")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.HasUUID("anything")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("HasUUID on empty store should be false")
	}
	if _, _, err := store.Rewind("anything"); err == nil {
		t.Errorf("Rewind on empty store should error")
	}
}

// ─── Case 9: no UUID set on engine → no snapshot captured ─

// Sub-agent submits don't set a user uuid; the SnapshotFile callback
// must short-circuit to a no-op so we don't leak unattributed blobs.
func TestRewindE2E_NoUUID_NoCapture(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")
	mustWrite(t, target, "v0")

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w1", target, "v1"), textTurn("ack"),
	)
	h := newRewindHarness(t, prov)
	// NB: do NOT call SetCurrentUserUUID — leave it as "".
	events := drainAll(h.eng.Submit(context.Background(), "no-uuid"))
	if !hasDone(events) {
		t.Fatalf("Submit did not finish")
	}

	entries, err := h.store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("no UUID set should mean no snapshot entries; got %d", len(entries))
	}
}

// ─── Case 10: HasUUID true after a real capture ────────────

func TestRewindE2E_HasUUID_AfterCapture(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")
	mustWrite(t, target, "v0")

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w1", target, "v1"), textTurn("ack"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-X", "p1")

	ok, err := h.store.HasUUID("uuid-X")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Errorf("HasUUID should be true after capture")
	}
	if other, _ := h.store.HasUUID("never"); other {
		t.Errorf("HasUUID for unrelated uuid should be false")
	}
}

// ─── Case 11: multiple captures of same (uuid,path) idempotent ─

// Two Edits to the same file under the same uuid should produce only
// one index entry (the pre-first-edit state). The Capture API is
// idempotent per (uuid, path).
func TestRewindE2E_IdempotentCapturePerUUIDPath(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "file.txt")
	mustWrite(t, target, "v0")

	prov := newReplayable(
		toolUseTurn("r1", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w1", target, "v1"), textTurn("a1"),
	)
	h := newRewindHarness(t, prov)
	h.runWithUUID(t, "uuid-A", "p1")

	prov.appendTurns(
		toolUseTurn("r2", "Read", `{"file_path":"`+target+`"}`),
		writeUseTurn("w2", target, "v2"), textTurn("a2"))
	h.runWithUUID(t, "uuid-A", "p2") // same UUID

	entries, err := h.store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.UUID == "uuid-A" && e.Path == target {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 idempotent entry; got %d", count)
	}
	// Rewind still gives v0 (the original pre-first-edit content).
	if _, _, err := h.store.Rewind("uuid-A"); err != nil {
		t.Fatal(err)
	}
	if got := readBytes(t, target); got != "v0" {
		t.Errorf("rewind should restore v0; got %q", got)
	}
}
