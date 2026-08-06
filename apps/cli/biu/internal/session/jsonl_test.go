package session

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOpenAppendClose(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{Type: "user_message", Content: "hi"},
		{Type: "assistant_delta", Content: "Hello "},
		{Type: "assistant_delta", Content: "world"},
		{Type: "end", Reason: "end_turn"},
	} {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	path := w.Path()
	w.Close()

	if !strings.Contains(path, "proj-1") {
		t.Errorf("project hash not in path: %s", path)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var n int
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		n++
	}
	if n != 4 {
		t.Errorf("got %d lines, want 4", n)
	}
}
