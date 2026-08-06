package rss

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestExportArchive(t *testing.T) {
	a := newPGApp(t) // skips without DATABASE_URL
	ctx := withCaller(context.Background(), "export-"+t.Name())

	// Seed one feed so the OPML + entries have something to export.
	// Idempotent: a prior run leaves the feed in the live DB, which is fine —
	// the export test only needs ≥1 feed present.
	if _, err := a.Invoke(ctx, "feeds_add",
		json.RawMessage(`{"url":"https://export.example/x.xml","title":"Export Feed"}`)); err != nil &&
		!strings.Contains(err.Error(), "already subscribed") {
		t.Fatalf("seed feed: %v", err)
	}

	out, err := a.Invoke(ctx, "export_archive", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("export_archive: %v", err)
	}
	m := out.(map[string]any)
	b64, _ := m["archive_b64"].(string)
	if b64 == "" {
		t.Fatal("empty archive")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if m["size"].(int) != len(raw) {
		t.Fatalf("size mismatch: %v vs %d", m["size"], len(raw))
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}

	// All five export members + README must be present.
	for _, want := range []string{"opml.xml", "entries.jsonl", "marks.csv", "rules.json", "README.txt"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s in archive (have %v)", want, keys(got))
		}
	}
	// OPML carries the seeded feed.
	if !strings.Contains(got["opml.xml"], "https://export.example/x.xml") {
		t.Errorf("opml missing seeded feed: %s", got["opml.xml"])
	}
	// marks.csv has the header even with zero marks.
	if !strings.HasPrefix(got["marks.csv"], "entry_id,mark,created_at") {
		t.Errorf("marks.csv header wrong: %q", got["marks.csv"])
	}
	// counts reported.
	counts := m["counts"].(map[string]int)
	if counts["feeds"] < 1 {
		t.Errorf("expected >=1 feed, counts=%v", counts)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
