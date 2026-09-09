package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testFilename is the fake archive name used across these tests.
const testFilename = "biu_0.9.0_Darwin_arm64.tar.gz"

// buildArchive packs the given file contents into a GoReleaser-style
// tar.gz (biu + LICENSE at the root).
func buildArchive(t *testing.T, biuBody string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := []struct{ name, body string }{
		{"biu", biuBody},
		{"LICENSE", "fake license"},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: 0o755, Size: int64(len(f.body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeDownload serves the archive + checksums.txt from in-memory URLs.
func fakeDownload(t *testing.T, archive []byte, filename string) func(*http.Request) (*http.Response, error) {
	t.Helper()
	sum := sha256.Sum256(archive)
	checksums := hex.EncodeToString(sum[:]) + "  " + filename + "\n"
	respond := func(body string) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}
	}
	return func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/biu.tar.gz":
			return respond(string(archive)), nil
		case "/checksums.txt":
			return respond(checksums), nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
}

func testOptions(t *testing.T, do func(*http.Request) (*http.Response, error)) Options {
	t.Helper()
	dir := t.TempDir()
	// A fake "current binary" to be swapped.
	exe := filepath.Join(dir, "biu")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{
		AssetURL:      "http://dl.test/biu.tar.gz",
		AssetFilename: testFilename,
		ChecksumsURL:  "http://dl.test/checksums.txt",
		HTTPDo:        do,
		ExePath:       exe,
		LockPath:      filepath.Join(dir, "update.lock"),
	}
}

func TestApplySwapsBinary(t *testing.T) {
	archive := buildArchive(t, "new binary v0.9.0")
	opt := testOptions(t, fakeDownload(t, archive, testFilename))

	if err := Apply(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(opt.ExePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary v0.9.0" {
		t.Errorf("binary not swapped, content = %q", got)
	}
	// Old binary survives as .old for rollback.
	old, err := os.ReadFile(opt.ExePath + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "old binary" {
		t.Errorf(".old content = %q", old)
	}
	// Mode preserved.
	st, _ := os.Stat(opt.ExePath)
	if st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", st.Mode().Perm())
	}
	// Lock released.
	if _, err := os.Stat(opt.LockPath); !os.IsNotExist(err) {
		t.Errorf("lock not cleaned up: %v", err)
	}
}

func TestApplyShaMismatchRefuses(t *testing.T) {
	archive := buildArchive(t, "tampered")
	do := func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "checksums.txt") {
			// Wrong digest for the archive we serve.
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("a", 64) + "  " + testFilename + "\n")),
				Header:     http.Header{},
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
			Header:     http.Header{},
		}, nil
	}
	opt := testOptions(t, do)

	err := Apply(context.Background(), opt)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("sha mismatch should refuse, got %v", err)
	}
	// Original binary untouched.
	got, _ := os.ReadFile(opt.ExePath)
	if string(got) != "old binary" {
		t.Errorf("binary must stay intact, content = %q", got)
	}
}

func TestApplyInlineSha(t *testing.T) {
	archive := buildArchive(t, "inline sha")
	sum := sha256.Sum256(archive)
	do := func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
			Header:     http.Header{},
		}, nil
	}
	opt := testOptions(t, do)
	opt.AssetSha256 = hex.EncodeToString(sum[:])
	opt.ChecksumsURL = "" // inline only

	if err := Apply(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
}

func TestApplySingleFlightLock(t *testing.T) {
	archive := buildArchive(t, "x")
	opt := testOptions(t, fakeDownload(t, archive, testFilename))

	// Pre-existing fresh lock → refusal.
	if err := os.WriteFile(opt.LockPath, []byte("pid=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Apply(context.Background(), opt)
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("fresh lock should refuse, got %v", err)
	}
	// Stale lock is stolen (backdate mtime).
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(opt.LockPath, past, past); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), opt); err != nil {
		t.Fatalf("stale lock should be stolen: %v", err)
	}
}

func TestApplyRequiresShaSource(t *testing.T) {
	opt := testOptions(t, nil)
	opt.AssetSha256 = ""
	opt.ChecksumsURL = ""
	err := Apply(context.Background(), opt)
	if err == nil || !strings.Contains(err.Error(), "sha256 source") {
		t.Fatalf("missing sha source should error, got %v", err)
	}
}
