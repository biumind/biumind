// HTTP handler tests for the notes search endpoint — wires Store +
// Verifier and exercises the route via httptest.
//
// Skips when DATABASE_URL unset (same convention as internal/files tests).

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testJWTSecret   = "biumind-note-api-test-secret-32+chars"
	testJWTIssuer   = "https://identity.test"
	testJWTAudience = "biumind-api"
)

type apiHarness struct {
	server *httptest.Server
	signer *bauth.Signer
	pool   *pgxpool.Pool
	st     *store.Store
}

func (h *apiHarness) close() {
	h.server.Close()
	h.pool.Close()
}

func (h *apiHarness) mintToken(uid uuid.UUID) string {
	tok, err := h.signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		panic(err)
	}
	return tok
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	st := store.New(pool)
	srv := NewServer(st,
		bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	return &apiHarness{
		server: httptest.NewServer(mux),
		signer: bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute),
		pool:   pool,
		st:     st,
	}
}

func (h *apiHarness) cleanupUser(t *testing.T, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.note_notes WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup notes: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.note_notebooks WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup notebooks: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.events WHERE scope = $1`, "note:user:"+uid.String()); err != nil {
		t.Fatalf("cleanup events: %v", err)
	}
}

func (h *apiHarness) get(t *testing.T, path, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", h.server.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestSearchAPI_RejectsEmptyQuery(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()

	status, body := h.get(t, "/v1/notes/search", h.mintToken(uid))
	if status != http.StatusBadRequest {
		t.Fatalf("missing q: expected 400 got %d (%v)", status, body)
	}
	status, _ = h.get(t, "/v1/notes/search?q=%20%20", h.mintToken(uid))
	if status != http.StatusBadRequest {
		t.Fatalf("blank q: expected 400 got %d", status)
	}
}

func TestSearchAPI_RequiresAuth(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	status, _ := h.get(t, "/v1/notes/search?q=x", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", status)
	}
}

func TestSearchAPI_HitShapeAndLimitClamp(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	defer h.cleanupUser(t, uid)

	token := "紫电青霜"
	if _, _, err := h.st.CreateNote(context.Background(), store.CreateNoteInput{
		UserID: uid, Title: "王勃的句子", ContentMD: token + "，王将军之武库。", ActorID: uid.String(),
	}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// limit 超过上限 50 应收敛而不是报错。
	status, body := h.get(t, "/v1/notes/search?q="+token+"&limit=500", h.mintToken(uid))
	if status != http.StatusOK {
		t.Fatalf("expected 200 got %d (%v)", status, body)
	}
	results, ok := body["results"].([]any)
	if !ok {
		t.Fatalf("missing results array: %v", body)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d (%v)", len(results), body)
	}
	r := results[0].(map[string]any)
	for _, k := range []string{"id", "notebook_id", "title", "is_todo", "updated_at", "snippet", "rank"} {
		if _, ok := r[k]; !ok {
			t.Errorf("result missing key %q: %v", k, r)
		}
	}
	if r["title"] != "王勃的句子" {
		t.Errorf("unexpected title: %v", r["title"])
	}
	if r["notebook_id"] != nil {
		t.Errorf("root note should serialize notebook_id as null, got %v", r["notebook_id"])
	}
}

// TestSearchAPI_StaticRouteWins — /v1/notes/search 不能被 {id} 抢走：
// 若被 {id} 匹配，"search" 不是合法 uuid → 400 bad_id 而非 400 bad_query。
func TestSearchAPI_StaticRouteWins(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	status, body := h.get(t, "/v1/notes/search", h.mintToken(uid))
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", status)
	}
	if body["error"].(map[string]any)["code"] != "bad_query" {
		t.Fatalf("route fell through to {id} handler: %v", body)
	}
}
