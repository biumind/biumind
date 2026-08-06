// HTTP handler tests for the N3 personal-notes retrieval path
// (include_notes). No DB needed: BM25 stays nil so the wiki/vector/graph
// lanes short-circuit and only the notes lane (stubbed noteSearcher) runs.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	notestore "github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/biumind/biumind/services/brain/internal/search/decay"
	"github.com/google/uuid"
)

const (
	notesTestJWTSecret   = "biumind-search-api-test-secret-32+chars"
	notesTestJWTIssuer   = "biumind-test"
	notesTestJWTAudience = "biumind-brain"
)

// stubNoteSearcher implements noteSearcher; records the userID each call
// received so tests can assert the caller's uid is always threaded through
// (strict per-user isolation of personal notes).
type stubNoteSearcher struct {
	hits   []notestore.SearchHit
	err    error
	calls  int
	gotUID uuid.UUID
}

func (s *stubNoteSearcher) SearchNotes(_ context.Context, userID uuid.UUID, _ string, _ int) ([]notestore.SearchHit, error) {
	s.calls++
	s.gotUID = userID
	return s.hits, s.err
}

func newNotesSearchHarness(t *testing.T, ns noteSearcher) (*httptest.Server, *bauth.Signer) {
	t.Helper()
	srv := NewServer(nil, nil, decay.New(30),
		bauth.NewVerifier(notesTestJWTSecret, notesTestJWTIssuer, notesTestJWTAudience),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if ns != nil {
		srv = srv.WithNotes(ns)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return httptest.NewServer(mux),
		bauth.NewSigner(notesTestJWTSecret, notesTestJWTIssuer, notesTestJWTAudience, 5*time.Minute)
}

func postSearch(t *testing.T, server *httptest.Server, signer *bauth.Signer, uid uuid.UUID, payload map[string]any) (int, map[string]any) {
	t.Helper()
	tok, err := signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", server.URL+"/v1/search", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSearchNotes_PrivacyDefaultOff(t *testing.T) {
	stub := &stubNoteSearcher{hits: []notestore.SearchHit{{
		ID: uuid.New(), Title: "秘密笔记", Rank: 0.9, UpdatedAt: time.Now(),
	}}}
	server, signer := newNotesSearchHarness(t, stub)
	defer server.Close()

	// 不传 include_notes —— 默认 false，notes 路不得触发。
	status, body := postSearch(t, server, signer, uuid.New(), map[string]any{
		"query": "秘密", "scope": "wiki",
	})
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	if _, present := body["notes"]; present {
		t.Fatalf("include_notes default false must not emit notes: %v", body)
	}
	if stub.calls != 0 {
		t.Fatalf("notes searcher must not be called, got %d calls", stub.calls)
	}

	// 显式 false 同样关闭。
	status, body = postSearch(t, server, signer, uuid.New(), map[string]any{
		"query": "秘密", "scope": "all", "include_notes": false,
	})
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	if _, present := body["notes"]; present {
		t.Fatalf("include_notes=false must not emit notes: %v", body)
	}
	if stub.calls != 0 {
		t.Fatalf("notes searcher must not be called, got %d calls", stub.calls)
	}
}

func TestSearchNotes_OptInCarriesCallerUID(t *testing.T) {
	noteID := uuid.New()
	updated := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	stub := &stubNoteSearcher{hits: []notestore.SearchHit{{
		ID: noteID, Title: "笔记", Snippet: "命中<mark>片段</mark>",
		Rank: 0.5, UpdatedAt: updated,
	}}}
	server, signer := newNotesSearchHarness(t, stub)
	defer server.Close()

	uid := uuid.New()
	status, body := postSearch(t, server, signer, uid, map[string]any{
		"query": "片段", "scope": "wiki", "include_notes": true,
	})
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	// 用户隔离：调 note 域必须带 caller user_id。
	if stub.calls != 1 || stub.gotUID != uid {
		t.Fatalf("notes search must carry caller uid %v, got calls=%d uid=%v", uid, stub.calls, stub.gotUID)
	}
	notes, _ := body["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("expected 1 note hit: %v", body)
	}
	h, _ := notes[0].(map[string]any)
	if h["id"] != noteID.String() || h["title"] != "笔记" ||
		h["snippet"] != "命中<mark>片段</mark>" || h["updated_at"] != updated.Format(time.RFC3339) {
		t.Fatalf("note hit shape wrong: %v", h)
	}
	if _, ok := h["score"].(float64); !ok {
		t.Fatalf("note hit missing numeric score: %v", h)
	}
}

func TestSearchNotes_NilStoreKeepsPathOff(t *testing.T) {
	server, signer := newNotesSearchHarness(t, nil)
	defer server.Close()

	status, body := postSearch(t, server, signer, uuid.New(), map[string]any{
		"query": "x", "scope": "wiki", "include_notes": true,
	})
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	if _, present := body["notes"]; present {
		t.Fatalf("nil notes store ⇒ path off, got %v", body)
	}
}

func TestSearchNotes_StoreErrorDegradesGracefully(t *testing.T) {
	stub := &stubNoteSearcher{err: errors.New("boom")}
	server, signer := newNotesSearchHarness(t, stub)
	defer server.Close()

	status, body := postSearch(t, server, signer, uuid.New(), map[string]any{
		"query": "x", "scope": "all", "include_notes": true,
	})
	if status != http.StatusOK {
		t.Fatalf("notes failure must not fail the search: %d (%v)", status, body)
	}
	if _, present := body["notes"]; present {
		t.Fatalf("failed notes lane should emit no notes array: %v", body)
	}
}
