package compact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTracker is a deterministic FileTracker for tests.
type fakeTracker struct{ paths []string }

func (f *fakeTracker) TrackedFiles() []string { return f.paths }

func TestBuildFileAttachments_emptyTracker(t *testing.T) {
	if got := BuildFileAttachments(&fakeTracker{}); got != nil {
		t.Errorf("empty tracker should return nil, got %v", got)
	}
	if got := BuildFileAttachments(nil); got != nil {
		t.Errorf("nil tracker should return nil")
	}
}

func TestBuildFileAttachments_readsFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.md")
	if err := os.WriteFile(a, []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("# B"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BuildFileAttachments(&fakeTracker{paths: []string{a, b}})
	if len(got) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(got))
	}
	// Sorted alphabetically: a.go before b.md.
	if !strings.HasSuffix(got[0].Path, "a.go") {
		t.Errorf("first attachment should be a.go, got %s", got[0].Path)
	}
	if got[0].Content != "package a" {
		t.Errorf("a.go content = %q", got[0].Content)
	}
	if got[0].WasTruncated {
		t.Error("small file should not truncate")
	}
}

func TestBuildFileAttachments_skipsMissing(t *testing.T) {
	dir := t.TempDir()
	exists := filepath.Join(dir, "exists.go")
	missing := filepath.Join(dir, "missing.go")
	_ = os.WriteFile(exists, []byte("ok"), 0o644)

	got := BuildFileAttachments(&fakeTracker{paths: []string{exists, missing}})
	if len(got) != 1 {
		t.Errorf("missing file should be skipped, got %d attachments", len(got))
	}
}

func TestBuildFileAttachments_truncatesLarge(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	body := strings.Repeat("x", MaxFileAttachmentBytes+1024)
	_ = os.WriteFile(big, []byte(body), 0o644)

	got := BuildFileAttachments(&fakeTracker{paths: []string{big}})
	if len(got) != 1 {
		t.Fatal("want 1 attachment")
	}
	if !got[0].WasTruncated {
		t.Error("big file should be truncated")
	}
	if len(got[0].Content) != MaxFileAttachmentBytes {
		t.Errorf("truncated len = %d, want %d", len(got[0].Content), MaxFileAttachmentBytes)
	}
	if got[0].SizeBytes != int64(len(body)) {
		t.Errorf("SizeBytes = %d, want %d", got[0].SizeBytes, len(body))
	}
}

func TestBuildFileAttachments_capsCount(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < MaxFileAttachmentCount+5; i++ {
		p := filepath.Join(dir, "f-"+string(rune('a'+i))+".txt")
		_ = os.WriteFile(p, []byte("x"), 0o644)
		paths = append(paths, p)
	}
	got := BuildFileAttachments(&fakeTracker{paths: paths})
	if len(got) != MaxFileAttachmentCount {
		t.Errorf("want %d attachments (cap), got %d", MaxFileAttachmentCount, len(got))
	}
}

func TestFileAttachmentsAsMessages_buildsSystemRole(t *testing.T) {
	atts := []FileAttachment{{
		Path: "/x.go", Content: "package x", SizeBytes: 9,
	}}
	msgs := FileAttachmentsAsMessages(atts)
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if string(msgs[0].Role) != "system" {
		t.Errorf("Role = %q, want system", msgs[0].Role)
	}
	body := msgs[0].Content[0].Text
	if !strings.Contains(body, `path="/x.go"`) {
		t.Errorf("body missing path attr: %s", body)
	}
	if !strings.Contains(body, "package x") {
		t.Errorf("body missing content: %s", body)
	}
	if !strings.Contains(body, `size_bytes=9`) {
		t.Errorf("body missing size_bytes: %s", body)
	}
	if strings.Contains(body, `truncated`) {
		t.Errorf("non-truncated should not include truncated attr: %s", body)
	}
}

func TestFileAttachmentsAsMessages_truncatedAttr(t *testing.T) {
	atts := []FileAttachment{{
		Path: "/big.txt", Content: "head", SizeBytes: 1000, WasTruncated: true,
	}}
	body := FileAttachmentsAsMessages(atts)[0].Content[0].Text
	if !strings.Contains(body, `truncated="true"`) {
		t.Errorf("body should include truncated attr: %s", body)
	}
	if !strings.Contains(body, "996 bytes truncated") {
		t.Errorf("body should include byte count of truncation: %s", body)
	}
}

func TestFileAttachmentsAsMessages_emptyInput(t *testing.T) {
	if got := FileAttachmentsAsMessages(nil); got != nil {
		t.Errorf("nil input → nil, got %v", got)
	}
}
