package rankings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"rankings.boards", "rankings.snapshots", "rankings.items_seen"} {
		var ok bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`, table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Skipf("%s missing; apply migrations", table)
		}
	}
	return pool
}

func TestStore_SeedBoardsPresent(t *testing.T) {
	store := NewStore(openDB(t))
	boards, err := store.ListBoards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) < 40 {
		t.Errorf("expected ≥40 seed boards (post-00008), got %d", len(boards))
	}
	have := map[string]bool{}
	for _, b := range boards {
		have[b.ID] = true
	}
	// 00008 removed hacker-news/sspai/yahoo-finance (perma-500
	// upstream); core seeds we still expect.
	for _, want := range []string{"weibo", "zhihu", "baidu", "douyin", "github-trending-today"} {
		if !have[want] {
			t.Errorf("missing seed board %q", want)
		}
	}
}

func TestStore_IngestSnapshot_DetectsNewItems(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	snap1 := &Snapshot{
		BoardID: "weibo",
		Items: []Item{
			{ID: "1", Title: "首次见到的标题 A", URL: "https://s.weibo.com/a"},
			{ID: "2", Title: "首次见到的标题 B", URL: "https://s.weibo.com/b"},
		},
	}
	new1, err := store.IngestSnapshot(ctx, snap1)
	if err != nil {
		t.Fatal(err)
	}
	if len(new1) != 2 {
		t.Errorf("first call: new = %d, want 2", len(new1))
	}

	// Same items again — none new.
	new2, _ := store.IngestSnapshot(ctx, snap1)
	if len(new2) != 0 {
		t.Errorf("second call: new = %d, want 0", len(new2))
	}

	// Mixed — 1 old + 1 new.
	snap3 := &Snapshot{
		BoardID: "weibo",
		Items: []Item{
			{ID: "1", Title: "首次见到的标题 A", URL: "https://s.weibo.com/a"},
			{ID: "3", Title: "新进榜标题 C", URL: "https://s.weibo.com/c"},
		},
	}
	new3, _ := store.IngestSnapshot(ctx, snap3)
	if len(new3) != 1 || new3[0].Title != "新进榜标题 C" {
		t.Errorf("third call: new = %+v, want C", new3)
	}

	// Cleanup
	_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.items_seen WHERE board_id='weibo'`)
	_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.snapshots WHERE board_id='weibo'`)
}

func TestStore_IsItemNew(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	defer func() {
		_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.items_seen WHERE board_id='zhihu'`)
		_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.snapshots WHERE board_id='zhihu'`)
	}()

	store.IngestSnapshot(ctx, &Snapshot{BoardID: "zhihu", Items: []Item{{Title: "A", URL: "https://www.zhihu.com/q/1"}}})
	isNew, err := store.IsItemNew(ctx, "zhihu", "A")
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("should be new (just inserted)")
	}
	isNew, _ = store.IsItemNew(ctx, "zhihu", "Never inserted")
	if isNew {
		t.Error("never-seen title should not be new")
	}
}

func TestStore_LatestPrevious(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	defer func() {
		_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.items_seen WHERE board_id='ifeng'`)
		_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.snapshots WHERE board_id='ifeng'`)
	}()

	store.IngestSnapshot(ctx, &Snapshot{BoardID: "ifeng", Items: []Item{{Title: "v1", URL: "https://www.ifeng.com/1"}}})
	// Force a tiny gap so captured_at differs.
	store.pool.Exec(ctx, `UPDATE rankings.snapshots SET captured_at = captured_at - interval '1 minute' WHERE board_id='ifeng'`)
	store.IngestSnapshot(ctx, &Snapshot{BoardID: "ifeng", Items: []Item{{Title: "v2", URL: "https://www.ifeng.com/2"}}})

	latest, err := store.LatestSnapshot(ctx, "ifeng")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Items[0].Title != "v2" {
		t.Errorf("latest = %v", latest.Items)
	}
	prev, err := store.PreviousSnapshot(ctx, "ifeng")
	if err != nil {
		t.Fatal(err)
	}
	if prev.Items[0].Title != "v1" {
		t.Errorf("prev = %v", prev.Items)
	}
}

func TestScheduler_EndToEnd(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	defer func() {
		_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.items_seen WHERE board_id='sspai'`)
		_, _ = store.pool.Exec(ctx, `DELETE FROM rankings.snapshots WHERE board_id='sspai'`)
		_, _ = store.pool.Exec(ctx, `UPDATE rankings.boards SET last_fetched_at = NULL, last_status='', last_error='', consecutive_failures=0 WHERE id='sspai'`)
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sspai","items":[
		  {"id":"a","title":"科技爱好者周刊","url":"https://www.sspai.com/blog/x"}
		]}`))
	}))
	defer srv.Close()

	// Force this board to use our test server by injecting a custom client.
	client := NewClient(srv.URL)

	// Make sspai due (newly migrated rows already due — last_fetched_at NULL).
	sched := NewScheduler(store, client)

	var cbHits []NewItem
	sched.OnNew = func(_ context.Context, items []NewItem) {
		for _, it := range items {
			if it.BoardID == "sspai" {
				cbHits = append(cbHits, it)
			}
		}
	}

	// Disable other boards for this test so we don't actually hit the public API.
	_, _ = store.pool.Exec(ctx, `UPDATE rankings.boards SET enabled = false WHERE id <> 'sspai'`)
	// Force sspai due (prod may have last_fetched_at recent).
	_, _ = store.pool.Exec(ctx, `UPDATE rankings.boards SET last_fetched_at = NULL WHERE id = 'sspai'`)
	defer store.pool.Exec(ctx, `UPDATE rankings.boards SET enabled = true`)

	stats, err := sched.RefreshAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.OK < 1 || stats.NewItems < 1 {
		t.Errorf("stats = %+v", stats)
	}
	if len(cbHits) == 0 {
		t.Errorf("OnNew not called for sspai")
	}
}
