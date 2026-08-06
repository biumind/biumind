package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stagedStore returns a fresh SnapshotStore in a TempDir + the
// HOME-shaped root path so tests can assert on disk layout.
func stagedStore(t *testing.T) (*SnapshotStore, string) {
	t.Helper()
	home := t.TempDir()
	store, err := NewSnapshotStore(home, "test-session")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, home
}

// TestSnapshotStore_NewCreatesDirs — root + blobs subdir created on
// construction.
func TestSnapshotStore_NewCreatesDirs(t *testing.T) {
	_, home := stagedStore(t)
	root := filepath.Join(home, ".biumind", "snapshots", "test-session")
	if st, err := os.Stat(filepath.Join(root, "blobs")); err != nil || !st.IsDir() {
		t.Errorf("blobs dir missing: %v", err)
	}
}

// TestSnapshotStore_Capture_ExistingFile — capture writes a blob +
// an index entry; second capture of the same (uuid, path) is a no-op.
func TestSnapshotStore_Capture_ExistingFile(t *testing.T) {
	store, _ := stagedStore(t)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "code.go")
	original := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Capture("uuid-1", target); err != nil {
		t.Fatalf("capture: %v", err)
	}
	// Second capture under same uuid+path: no-op (idempotent).
	if err := store.Capture("uuid-1", target); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	entries, err := store.Entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry; got %d", len(entries))
	}
	if !entries[0].Existed || entries[0].Size != int64(len(original)) {
		t.Errorf("entry shape: %+v", entries[0])
	}
}

// TestSnapshotStore_Capture_MissingFile — file that doesn't exist
// is captured as Existed=false (so rewind can later DELETE the file
// to undo a creation).
func TestSnapshotStore_Capture_MissingFile(t *testing.T) {
	store, _ := stagedStore(t)
	if err := store.Capture("uuid-2", "/tmp/never-existed.txt"); err != nil {
		t.Fatalf("capture missing: %v", err)
	}
	entries, _ := store.Entries()
	if len(entries) != 1 || entries[0].Existed {
		t.Errorf("missing file should record Existed=false: %+v", entries)
	}
}

// TestSnapshotStore_Rewind_Restores — capture, mutate the file,
// rewind: original content is restored.
func TestSnapshotStore_Rewind_Restores(t *testing.T) {
	store, _ := stagedStore(t)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "data.txt")
	original := "before edit\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Capture("uuid-A", target); err != nil {
		t.Fatal(err)
	}
	// Simulate Edit/Write mutating the file.
	if err := os.WriteFile(target, []byte("after edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	count, paths, err := store.Rewind("uuid-A")
	if err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if count != 1 || paths[0] != target {
		t.Errorf("rewind result: count=%d paths=%v", count, paths)
	}
	got, _ := os.ReadFile(target)
	if string(got) != original {
		t.Errorf("file not restored: %q", got)
	}
}

// TestSnapshotStore_Rewind_DeletesCreatedFile — when the snapshot
// records Existed=false, rewind removes the file.
func TestSnapshotStore_Rewind_DeletesCreatedFile(t *testing.T) {
	store, _ := stagedStore(t)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "new.txt")
	// Capture before file exists.
	if err := store.Capture("uuid-B", target); err != nil {
		t.Fatal(err)
	}
	// Simulate the user message creating the file.
	if err := os.WriteFile(target, []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	count, _, err := store.Rewind("uuid-B")
	if err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file rewound; got %d", count)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be deleted after rewind; stat err=%v", err)
	}
}

// TestSnapshotStore_Rewind_MultipleEdits_RestoresFirst — when multiple
// edits across multiple uuids happen, rewind to the EARLIEST target
// uuid restores each path's first-from-target snapshot (= state right
// before that uuid).
func TestSnapshotStore_Rewind_MultipleEdits_RestoresFirst(t *testing.T) {
	store, _ := stagedStore(t)
	tmp := t.TempDir()
	target := filepath.Join(tmp, "doc.md")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// uuid-A captures v1.
	_ = store.Capture("uuid-A", target)
	// First edit → v2.
	_ = os.WriteFile(target, []byte("v2"), 0o644)
	// uuid-B captures v2.
	_ = store.Capture("uuid-B", target)
	// Second edit → v3.
	_ = os.WriteFile(target, []byte("v3"), 0o644)

	// Rewind to uuid-A: file should go back to v1.
	count, _, err := store.Rewind("uuid-A")
	if err != nil || count != 1 {
		t.Fatalf("rewind A: count=%d err=%v", count, err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "v1" {
		t.Errorf("rewind to A should yield v1; got %q", got)
	}
}

// TestSnapshotStore_HasUUID — fast check used by --rewind-files
// before mutating the filesystem.
func TestSnapshotStore_HasUUID(t *testing.T) {
	store, _ := stagedStore(t)
	if ok, _ := store.HasUUID("none"); ok {
		t.Errorf("empty store should not claim any uuid")
	}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "x")
	_ = os.WriteFile(p, []byte("y"), 0o644)
	_ = store.Capture("uuid-1", p)
	if ok, _ := store.HasUUID("uuid-1"); !ok {
		t.Errorf("HasUUID should find captured uuid")
	}
	if ok, _ := store.HasUUID("ghost"); ok {
		t.Errorf("HasUUID should miss unknown uuid")
	}
}

// TestSnapshotStore_Rewind_UnknownUUID — surfaces an error so the
// CLI flag layer can show the user a clear message.
func TestSnapshotStore_Rewind_UnknownUUID(t *testing.T) {
	store, _ := stagedStore(t)
	_, _, err := store.Rewind("ghost")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected error mentioning the missing uuid: %v", err)
	}
}

// TestSnapshotStore_Capture_Dedupes — two captures with the same
// content reuse the same blob (sha256-keyed).
func TestSnapshotStore_Capture_Dedupes(t *testing.T) {
	store, home := stagedStore(t)
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a.txt")
	b := filepath.Join(tmp, "b.txt")
	_ = os.WriteFile(a, []byte("same"), 0o644)
	_ = os.WriteFile(b, []byte("same"), 0o644)
	_ = store.Capture("uuid-1", a)
	_ = store.Capture("uuid-1", b) // different path, but same content under uuid-1
	// Same uuid + path is no-op so second Capture for `b` is allowed.

	blobsDir := filepath.Join(home, ".biumind", "snapshots", "test-session", "blobs")
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("identical content should yield 1 blob; got %d", len(entries))
	}
}

// TestEvent_UUID_RoundTrips — UUID field round-trips through JSONL
// encoding/decoding (locks the JSON tag).
func TestEvent_UUID_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	id, err := w.AppendWithUUID(Event{Type: "user_message", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatalf("AppendWithUUID should auto-generate a UUID for user_message")
	}
	body, _ := os.ReadFile(w.Path())
	if !strings.Contains(string(body), `"uuid":"`+id+`"`) {
		t.Errorf("uuid field not in JSON: %s", body)
	}
}

// TestAppend_NonUserMessage_NoUUID — only user_message events get
// auto-uuids; tool_use / assistant_message rows stay slim.
func TestAppend_NonUserMessage_NoUUID(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, "x")
	defer w.Close()
	id, _ := w.AppendWithUUID(Event{Type: "tool_use", Name: "Bash"})
	if id != "" {
		t.Errorf("non-user_message should not auto-generate uuid; got %q", id)
	}
}
