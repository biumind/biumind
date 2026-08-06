package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/tools/web"
)

func TestPickByExtension(t *testing.T) {
	p := NewPool("/tmp", []ServerSpec{
		{Name: "gopls", Command: "gopls", Extensions: []string{".go"}},
		{Name: "pyright", Command: "pyright", Extensions: []string{".py"}},
	})
	if got := p.pick(web.LSPRequest{FilePath: "/x.go"}); got == nil || got.Name != "gopls" {
		t.Errorf(".go should pick gopls; got %+v", got)
	}
	if got := p.pick(web.LSPRequest{FilePath: "/x.py"}); got == nil || got.Name != "pyright" {
		t.Errorf(".py should pick pyright; got %+v", got)
	}
	if got := p.pick(web.LSPRequest{FilePath: "/x.rb"}); got != nil {
		t.Errorf(".rb has no server; got %+v", got)
	}
	// workspaceSymbol always uses first server.
	if got := p.pick(web.LSPRequest{Operation: "workspaceSymbol"}); got == nil || got.Name != "gopls" {
		t.Errorf("workspaceSymbol should fall back to first; got %+v", got)
	}
}

func TestLSPNoServersConfigured(t *testing.T) {
	p := NewPool("/tmp", nil)
	if _, err := p.LSP(context.Background(), web.LSPRequest{
		Operation: "hover", FilePath: "x.go",
	}); err == nil {
		t.Errorf("expected error when no servers configured")
	}
}

func TestBuildMethodMapsOperations(t *testing.T) {
	cases := map[string]string{
		"goToDefinition":       "textDocument/definition",
		"findReferences":       "textDocument/references",
		"hover":                "textDocument/hover",
		"documentSymbol":       "textDocument/documentSymbol",
		"workspaceSymbol":      "workspace/symbol",
		"goToImplementation":   "textDocument/implementation",
		"prepareCallHierarchy": "textDocument/prepareCallHierarchy",
		"incomingCalls":        "callHierarchy/incomingCalls",
		"outgoingCalls":        "callHierarchy/outgoingCalls",
	}
	for op, want := range cases {
		got, params := buildMethod(web.LSPRequest{Operation: op, FilePath: "/x.go"})
		if got != want {
			t.Errorf("op %q → %q, want %q", op, got, want)
		}
		if params == nil {
			t.Errorf("op %q produced nil params", op)
		}
	}
	if got, _ := buildMethod(web.LSPRequest{Operation: "garbage"}); got != "" {
		t.Errorf("unsupported op should return empty method; got %q", got)
	}
}

func TestPathToURI(t *testing.T) {
	if got := pathToURI("/abs/x.go"); got != "file:///abs/x.go" {
		t.Errorf("uri = %q", got)
	}
	if got := pathToURI(""); got != "" {
		t.Errorf("empty path should yield empty uri; got %q", got)
	}
}

func TestLanguageID(t *testing.T) {
	cases := map[string]string{
		"foo.go":  "go",
		"x.PY":    "python",
		"y.tsx":   "typescriptreact",
		"z.weird": "plaintext",
	}
	for p, want := range cases {
		if got := languageID(p); got != want {
			t.Errorf("languageID(%q)=%q, want %q", p, got, want)
		}
	}
}

func TestReadFrameRoundTrip(t *testing.T) {
	payload := map[string]any{"id": 1, "method": "test"}
	body, _ := json.Marshal(payload)
	var buf bytes.Buffer
	buf.WriteString("Content-Length: ")
	buf.WriteString(itoaHack(len(body)))
	buf.WriteString("\r\n\r\n")
	buf.Write(body)
	r := bufio.NewReader(&buf)
	got, err := readFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("frame roundtrip lost bytes: %q vs %q", got, body)
	}
}

// Tracking server: replaces server.notify with a recorder so we can
// inspect the wire shape of didOpen / didChange.
type recordingServer struct {
	server
	notes []recordedNote
}

type recordedNote struct {
	method string
	params map[string]any
}

func newRecordingServer() *recordingServer {
	return &recordingServer{
		server: server{docs: map[string]*docState{}},
	}
}

// We can't patch a method, but we can call syncDocument directly and
// inspect the internal map state — that proves docOpen/didChange logic
// without the stdin pipe.

func TestSyncDocumentDidOpenThenDidChange(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newRecordingServer()
	s.syncDocument(path)
	uri := pathToURI(path)
	if s.docs[uri] == nil {
		t.Fatal("first sync should record doc state")
	}
	if s.docs[uri].version != 1 {
		t.Errorf("initial version=%d, want 1", s.docs[uri].version)
	}

	// Unchanged content → no version bump.
	s.syncDocument(path)
	if s.docs[uri].version != 1 {
		t.Errorf("unchanged content bumped version: %d", s.docs[uri].version)
	}

	// Modified file → version bump.
	_ = os.WriteFile(path, []byte("package main\nfunc f(){}\n"), 0o644)
	s.syncDocument(path)
	if s.docs[uri].version != 2 {
		t.Errorf("modified content version=%d, want 2", s.docs[uri].version)
	}

	// Touch invalidates the hash → next sync bumps even on identical bytes.
	s.Touch(path)
	prev := s.docs[uri].version
	s.syncDocument(path)
	if s.docs[uri].version != prev+1 {
		t.Errorf("Touch should force bump: %d → %d", prev, s.docs[uri].version)
	}
}

func TestPoolTouchPropagates(t *testing.T) {
	p := NewPool("/tmp", nil)
	srv := &server{docs: map[string]*docState{
		pathToURI("/tmp/x.go"): {hash: "abc", version: 1},
	}}
	p.servers["fake"] = srv
	p.Touch("/tmp/x.go")
	if got := srv.docs[pathToURI("/tmp/x.go")].hash; got != "" {
		t.Errorf("Touch should clear hash; got %q", got)
	}
}

func TestReadFrameMissingHeader(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\r\n"))
	if _, err := readFrame(r); err == nil {
		t.Errorf("missing header should error")
	}
}

func itoaHack(n int) string {
	buf := make([]byte, 0, 12)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
