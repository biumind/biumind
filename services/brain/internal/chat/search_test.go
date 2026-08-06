// Integration tests for chat message search. Skips when DATABASE_URL
// unset (same convention as store_test.go).

package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedThread + seedMessages — convenience helpers building a fixture
// thread with N success messages so the tests have something to search.
func seedThread(t *testing.T, s *Store, uid uuid.UUID, title string) uuid.UUID {
	t.Helper()
	model := "test-model"
	th, err := s.CreateThread(context.Background(), CreateThreadInput{
		UserID: uid, Title: title, Model: &model, SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	return th.ID
}

func seedMessage(t *testing.T, s *Store, threadID, uid uuid.UUID,
	role, content string, parts []byte, status string,
) uuid.UUID {
	t.Helper()
	if status == "" {
		status = "success"
	}
	m, err := s.CreateMessage(context.Background(), CreateMessageInput{
		ThreadID: threadID, UserID: uid, Role: role, Content: content,
		Parts: parts, Status: status,
	})
	if err != nil {
		t.Fatalf("seed msg: %v", err)
	}
	return m.ID
}

// ─── Trigger / index basic correctness ─────────────────────

func TestSearch_TriggerOnlyIndexesTerminalMessages(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "trigger test")

	// streaming → search_vector should stay NULL → not findable
	_ = seedMessage(t, s, tid, uid, "assistant",
		"unique-token-streaming-xyz", nil, "streaming")
	hits, _, err := s.SearchMessages(ctx, uid, SearchRequest{
		Query: "unique-token-streaming-xyz", Limit: 10,
	}, false)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("streaming message should not be searchable, got %d", len(hits))
	}

	// success → indexed → findable
	_ = seedMessage(t, s, tid, uid, "user",
		"unique-token-success-xyz", nil, "success")
	hits, _, _ = s.SearchMessages(ctx, uid, SearchRequest{
		Query: "unique-token-success-xyz", Limit: 10,
	}, false)
	if len(hits) != 1 {
		t.Errorf("success message expected 1 hit, got %d", len(hits))
	}
}

// ─── User scoping ─────────────────────────────────────────

func TestSearch_OnlyReturnsCurrentUserMessages(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uidA := uuid.New()
	uidB := uuid.New()
	token := "leak-test-" + uuid.New().String()

	tA := seedThread(t, s, uidA, "user A's thread")
	_ = seedMessage(t, s, tA, uidA, "user", "secret content "+token, nil, "success")

	tB := seedThread(t, s, uidB, "user B's thread")
	_ = seedMessage(t, s, tB, uidB, "user", "B's notes about "+token, nil, "success")

	// User A searches — should only see A's row.
	hits, _, _ := s.SearchMessages(ctx, uidA, SearchRequest{
		Query: token, Limit: 10,
	}, false)
	for _, h := range hits {
		if h.ThreadID == tB {
			t.Fatalf("LEAK: user A saw user B's thread %s", tB)
		}
	}
	if len(hits) == 0 {
		t.Errorf("user A should see at least their own message")
	}
}

// ─── Filters ──────────────────────────────────────────────

func TestSearch_ThreadFilter(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	t1 := seedThread(t, s, uid, "thread one")
	t2 := seedThread(t, s, uid, "thread two")
	token := "filter-thread-" + uuid.New().String()
	_ = seedMessage(t, s, t1, uid, "user", "from t1: "+token, nil, "success")
	_ = seedMessage(t, s, t2, uid, "user", "from t2: "+token, nil, "success")

	hits, _, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, ThreadID: &t1, Limit: 10,
	}, false)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit scoped to t1, got %d", len(hits))
	}
	if hits[0].ThreadID != t1 {
		t.Errorf("hit thread mismatch")
	}
}

func TestSearch_RoleFilter(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "role test")
	token := "role-filter-" + uuid.New().String()
	_ = seedMessage(t, s, tid, uid, "user", token, nil, "success")
	_ = seedMessage(t, s, tid, uid, "assistant", token+" reply", nil, "success")

	hits, _, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, Role: "assistant", Limit: 10,
	}, false)
	if len(hits) != 1 || hits[0].Role != "assistant" {
		t.Errorf("role filter: %+v", hits)
	}
}

func TestSearch_TimeRangeFilter(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "time test")
	token := "time-" + uuid.New().String()

	mid := seedMessage(t, s, tid, uid, "user", token, nil, "success")
	// Force created_at backwards.
	_, err := s.pool.Exec(ctx,
		`UPDATE chat.messages SET created_at = $1 WHERE id = $2`,
		time.Now().Add(-48*time.Hour), mid)
	if err != nil {
		t.Fatalf("age msg: %v", err)
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	hits, _, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, From: &cutoff, Limit: 10,
	}, false)
	if len(hits) != 0 {
		t.Errorf("from filter should exclude old msg: %+v", hits)
	}

	earlier := time.Now().Add(-72 * time.Hour)
	hits, _, _ = s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, From: &earlier, Limit: 10,
	}, false)
	if len(hits) != 1 {
		t.Errorf("from filter should include msg, got %d", len(hits))
	}
}

// ─── Ranking ──────────────────────────────────────────────

func TestSearch_RanksRelevantHigher(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "rank test")
	token := "rank-" + uuid.New().String()

	// One short message that's exactly the query → high score.
	hi := seedMessage(t, s, tid, uid, "user", token, nil, "success")
	// One long message with the query buried in noise → lower score.
	long := strings.Repeat("noise word filler text ", 200) + " " + token +
		" " + strings.Repeat("trailing more text ", 200)
	lo := seedMessage(t, s, tid, uid, "user", long, nil, "success")

	hits, _, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, Limit: 10,
	}, false)
	if len(hits) < 2 {
		t.Fatalf("expected ≥2 hits, got %d", len(hits))
	}
	// Find positions
	idxHi, idxLo := -1, -1
	for i, h := range hits {
		if h.MessageID == hi {
			idxHi = i
		}
		if h.MessageID == lo {
			idxLo = i
		}
	}
	if idxHi == -1 || idxLo == -1 {
		t.Fatalf("missing hits: hi=%d lo=%d", idxHi, idxLo)
	}
	if idxHi >= idxLo {
		t.Errorf("short exact match (idx=%d) should rank above long (idx=%d)", idxHi, idxLo)
	}
}

// ─── Highlight ────────────────────────────────────────────

func TestSearch_HighlightWrapsMatches(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "hl test")
	// 用 uuid-style token 作为锚点 — zhparser 对纯字母+数字单独分词,
	// 不会因为相邻中文上下文被切碎。中文上下文用来验证 ts_headline 在
	// 混合内容上能正确包裹。zhparser 对纯中文子串匹配的限制是已知问题
	// (e.g. 搜"图片"匹配不到"这张图片"), 文档化, 不在此测覆盖。
	token := "anchor" + strings.ReplaceAll(uuid.New().String(), "-", "")
	_ = seedMessage(t, s, tid, uid, "user",
		"这是一段包含 "+token+" 的文字，前后还有内容用于 ts_headline 测试。",
		nil, "success")

	hits, _, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, Limit: 10,
	}, true)
	if len(hits) == 0 {
		t.Fatalf("no hits for anchor token")
	}
	if !strings.Contains(hits[0].Snippet, "<mark>") {
		t.Errorf("snippet missing highlight: %q", hits[0].Snippet)
	}
}

// ─── parts JSON indexing ──────────────────────────────────

func TestSearch_FindsTextInsidePartsJSON(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "parts test")
	token := "parts-token-" + uuid.New().String()
	parts := []byte(`[
        {"type":"text","text":"intro line"},
        {"type":"thinking","text":"deliberation about ` + token + ` here"},
        {"type":"text","text":"final answer"}
    ]`)
	_ = seedMessage(t, s, tid, uid, "assistant",
		"shallow content", parts, "success")

	hits, _, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, Limit: 10,
	}, false)
	if len(hits) != 1 {
		t.Errorf("parts search: expected 1 hit, got %d", len(hits))
	}
}

// ─── Total + pagination ───────────────────────────────────

func TestSearch_TotalReportsAcrossPages(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	tid := seedThread(t, s, uid, "page test")
	token := "page-" + uuid.New().String()
	for i := 0; i < 5; i++ {
		_ = seedMessage(t, s, tid, uid, "user",
			token+" msg", nil, "success")
	}
	hits, total, _ := s.SearchMessages(ctx, uid, SearchRequest{
		Query: token, Limit: 2, Offset: 0,
	}, false)
	if len(hits) != 2 {
		t.Errorf("page1 hits: %d", len(hits))
	}
	if total != 5 {
		t.Errorf("total: %d want 5", total)
	}
}

// ─── Empty results ────────────────────────────────────────

func TestSearch_EmptyResultZeroTotal(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	hits, total, err := s.SearchMessages(ctx, uid, SearchRequest{
		Query: "nope-nothing-here-" + uuid.New().String(), Limit: 10,
	}, false)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 || total != 0 {
		t.Errorf("expected empty, got hits=%d total=%d", len(hits), total)
	}
}
