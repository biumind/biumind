package driver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarball builds a gzipped tar in memory from the entries map.
// Each map entry is path → file content (relative to extract root).
func makeTarball(t *testing.T, entries map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name:     name,
			Size:     int64(len(body)),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return &buf
}

func TestStaticDeployRoundtrip(t *testing.T) {
	root := t.TempDir()
	d := NewStatic(root, "https://example.com/static")
	tar := makeTarball(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a/b.txt":    "hello",
	})
	dep, err := d.Deploy(context.Background(), Plan{
		OwnerID: "u1",
		Kind:    KindStatic,
		Tarball: tar,
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.Status != "running" || !strings.HasPrefix(dep.URL, "https://example.com/static/") {
		t.Errorf("bad deployment: %+v", dep)
	}
	body, err := os.ReadFile(filepath.Join(root, dep.ID, "index.html"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(body) != "<h1>hi</h1>" {
		t.Errorf("content mismatch: %q", body)
	}
}

func TestStaticRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	d := NewStatic(root, "https://example.com/static")
	tar := makeTarball(t, map[string]string{
		"../escape.txt": "evil",
	})
	dep, err := d.Deploy(context.Background(), Plan{
		OwnerID: "u1",
		Kind:    KindStatic,
		Tarball: tar,
	})
	if err == nil {
		t.Fatalf("expected error, got dep=%+v", dep)
	}
	// Verify nothing landed outside root.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); statErr == nil {
		t.Errorf("file escaped to %s", filepath.Dir(root))
	}
}

func TestStaticDestroyRemovesFiles(t *testing.T) {
	root := t.TempDir()
	d := NewStatic(root, "https://example.com/static")
	tar := makeTarball(t, map[string]string{"index.html": "x"})
	dep, _ := d.Deploy(context.Background(), Plan{OwnerID: "u1", Kind: KindStatic, Tarball: tar})
	if err := d.Destroy(context.Background(), dep.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, dep.ID)); !os.IsNotExist(err) {
		t.Errorf("expected dir removed, stat err=%v", err)
	}
}
