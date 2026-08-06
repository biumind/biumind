package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func dbURL() string {
	if v := os.Getenv("AIGC_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	url := dbURL()
	if url == "skip" {
		t.Skip("AIGC_TEST_DATABASE_URL=skip")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

func newUser() uuid.UUID { return uuid.New() }

// resetUser 删指定 user 的所有 aigc 数据 (任务/输出 CASCADE; 不动 models/providers).
func resetUser(t *testing.T, s *Store, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `DELETE FROM aigc.tasks WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("reset tasks: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM aigc.characters WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("reset characters: %v", err)
	}
}

// ensureSeedProviderModel 保证测试用的 provider/model 存在 (UpsertModel/UpsertProvider).
// 用 wanx-2.6-t2i 作为通用测试模型.
func ensureSeedProviderModel(t *testing.T, s *Store) (providerCode, modelCode string) {
	t.Helper()
	ctx := context.Background()
	providerCode = "test-dashscope"
	modelCode = "test-wanx-2.6-t2i"

	if err := s.UpsertProvider(ctx, UpsertProviderArgs{
		Code: providerCode, Name: "Test DashScope",
		BaseURL:  "https://dashscope.aliyuncs.com",
		Priority: 100, Enabled: true,
	}); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	cfg, _ := json.Marshal(map[string]any{
		"aspect_ratios": []map[string]string{{"key": "16:9", "value": "16:9"}},
		"resolutions":   []map[string]string{{"key": "720p", "value": "720p"}},
	})
	if err := s.UpsertModel(ctx, UpsertModelArgs{
		Code: modelCode, Type: "image", DisplayName: "Test 通义万相 2.6",
		ProviderCode: providerCode, PriceCredits: 40,
		Config: cfg, Enabled: true, SortOrder: 100,
	}); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	return
}

// ════════════════════════════════════════════════════════════
// Tasks
// ════════════════════════════════════════════════════════════

func TestCreateTask_AndGet(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	task, err := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image",
		ModelCode: model, ProviderCode: prov,
		Prompt: "柯基在草地", Params: map[string]any{"aspect_ratio": "16:9"},
		CostCredits: 40,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Status != "pending" || task.Progress != 0 {
		t.Fatalf("init state wrong: %+v", task)
	}

	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "柯基在草地" || got.UserID != uid || got.CostCredits != 40 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetTask(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "x", CostCredits: 40,
	})

	// queued
	now := time.Now().UTC()
	if err := s.UpdateTaskStatus(ctx, UpdateTaskStatusArgs{
		ID: task.ID, Status: "queued", QueuedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	// running 50%
	prog := int16(50)
	if err := s.UpdateTaskStatus(ctx, UpdateTaskStatusArgs{
		ID: task.ID, Status: "running", Progress: &prog,
		StartedAt: &now, ExternalTaskID: "vendor-xyz",
	}); err != nil {
		t.Fatal(err)
	}
	// completed
	prog = 100
	if err := s.UpdateTaskStatus(ctx, UpdateTaskStatusArgs{
		ID: task.ID, Status: "completed", Progress: &prog, CompletedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetTask(ctx, task.ID)
	if got.Status != "completed" || got.Progress != 100 || got.ExternalTaskID != "vendor-xyz" {
		t.Fatalf("final state wrong: %+v", got)
	}
}

func TestUpdateTaskStatus_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateTaskStatus(context.Background(), UpdateTaskStatusArgs{
		ID: uuid.New(), Status: "completed",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListMyTasks_FiltersAndPagination(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	// 3 image + 2 video
	for i := 0; i < 3; i++ {
		_, err := s.CreateTask(ctx, CreateTaskArgs{
			UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
			Prompt: "image", CostCredits: 40,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// video model 也用 image model code, 类型按 task.type 过滤
	for i := 0; i < 2; i++ {
		_, err := s.CreateTask(ctx, CreateTaskArgs{
			UserID: uid, Type: "video", ModelCode: model, ProviderCode: prov,
			Prompt: "video", CostCredits: 40,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	all, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid})
	if len(all) != 5 {
		t.Errorf("all = %d, want 5", len(all))
	}
	imgs, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid, Types: []string{"image"}})
	if len(imgs) != 3 {
		t.Errorf("images = %d, want 3", len(imgs))
	}
	vids, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid, Types: []string{"video"}})
	if len(vids) != 2 {
		t.Errorf("videos = %d, want 2", len(vids))
	}
	// 状态过滤
	pending, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid, Statuses: []string{"pending"}})
	if len(pending) != 5 {
		t.Errorf("pending = %d, want 5", len(pending))
	}
	completed, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid, Statuses: []string{"completed"}})
	if len(completed) != 0 {
		t.Errorf("completed = %d, want 0", len(completed))
	}
}

func TestSetTaskVisibility_AndSoftDelete(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	other := newUser()
	resetUser(t, s, uid)
	resetUser(t, s, other)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	t1, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "mine", CostCredits: 40,
	})
	t2, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: other, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "other", CostCredits: 40,
	})

	// 改自己的 + 别人的 → 只改了自己的
	n, _ := s.SetTaskVisibility(ctx, uid, []uuid.UUID{t1.ID, t2.ID}, true)
	if n != 1 {
		t.Errorf("updated = %d, want 1", n)
	}
	got1, _ := s.GetTask(ctx, t1.ID)
	if !got1.IsPublic {
		t.Errorf("t1 should be public")
	}
	got2, _ := s.GetTask(ctx, t2.ID)
	if got2.IsPublic {
		t.Errorf("t2 should not be touched")
	}

	// 软删自己的
	n, _ = s.SoftDeleteTasks(ctx, uid, []uuid.UUID{t1.ID, t2.ID})
	if n != 1 {
		t.Errorf("deleted = %d, want 1", n)
	}
	got1, _ = s.GetTask(ctx, t1.ID)
	if got1.DeletedAt == nil {
		t.Errorf("t1 should be soft-deleted")
	}
	// 默认 ListMyTasks 不返回软删的
	mine, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid})
	if len(mine) != 0 {
		t.Errorf("mine after soft-delete = %d, want 0 (default excludes deleted)", len(mine))
	}
	// IncludeDeleted 能看到
	all, _ := s.ListMyTasks(ctx, ListMyTasksArgs{UserID: uid, IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("all incl deleted = %d, want 1", len(all))
	}
}

// ════════════════════════════════════════════════════════════
// TaskOutputs
// ════════════════════════════════════════════════════════════

func TestCreateTaskOutput_AndList(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "x", CostCredits: 40,
	})

	o1, err := s.CreateTaskOutput(ctx, CreateTaskOutputArgs{
		TaskID: task.ID, Idx: 0, Kind: "image",
		SHA256: "sha-abc", StorageURL: "cas:sha-abc",
		StorageKey: "outputs/sh/a/sha-abc.png",
		Width:      1024, Height: 1024, FileSize: 234567,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o1.SHA256 != "sha-abc" || o1.Width != 1024 {
		t.Fatalf("output bad: %+v", o1)
	}

	_, _ = s.CreateTaskOutput(ctx, CreateTaskOutputArgs{
		TaskID: task.ID, Idx: 1, Kind: "image",
		SHA256: "sha-def", StorageURL: "cas:sha-def",
		StorageKey: "outputs/sh/d/sha-def.png",
	})

	outs, err := s.ListTaskOutputs(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 || outs[0].Idx != 0 || outs[1].Idx != 1 {
		t.Fatalf("list: %+v", outs)
	}
}

// CASCADE 验证: 删除 task 时 outputs 一起删
func TestTaskCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "x", CostCredits: 40,
	})
	_, _ = s.CreateTaskOutput(ctx, CreateTaskOutputArgs{
		TaskID: task.ID, Idx: 0, Kind: "image",
		SHA256: "sha-xx", StorageURL: "cas:sha-xx", StorageKey: "k",
	})
	// 物理删除 task (软删不会 CASCADE; 这里 raw DELETE 验证 schema 设的 ON DELETE CASCADE)
	if _, err := s.pool.Exec(ctx, `DELETE FROM aigc.tasks WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}
	outs, _ := s.ListTaskOutputs(ctx, task.ID)
	if len(outs) != 0 {
		t.Errorf("cascade failed: outputs len = %d", len(outs))
	}
}

// ════════════════════════════════════════════════════════════
// Models / Providers
// ════════════════════════════════════════════════════════════

func TestUpsertAndListModels(t *testing.T) {
	s := newTestStore(t)
	prov, model := ensureSeedProviderModel(t, s)
	_ = prov
	ctx := context.Background()

	got, err := s.GetModel(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	if got.PriceCredits != 40 {
		t.Errorf("price = %d", got.PriceCredits)
	}

	imgs, err := s.ListModels(ctx, "image", false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range imgs {
		if m.Code == model {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("test model not in image list")
	}

	// upsert 修改
	if err := s.UpsertModel(ctx, UpsertModelArgs{
		Code: model, Type: "image", DisplayName: "Test 通义万相 2.6",
		ProviderCode: prov, PriceCredits: 50,
		Config: []byte(`{}`), Enabled: true, SortOrder: 100,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetModel(ctx, model)
	if got.PriceCredits != 50 {
		t.Errorf("upsert didn't update price: %d", got.PriceCredits)
	}
}

// ════════════════════════════════════════════════════════════
// Lineage DAG (★ MVP 必做)
// ════════════════════════════════════════════════════════════

func TestLineage_AddAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// A 派生 B (remix), B 派生 C (i2v)
	if err := s.AddLineageEdge(ctx, AddLineageEdgeArgs{
		ChildSHA: "B", ParentSHA: "A", Op: "remix",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLineageEdge(ctx, AddLineageEdgeArgs{
		ChildSHA: "C", ParentSHA: "B", Op: "i2v",
	}); err != nil {
		t.Fatal(err)
	}

	// 重复插同一边 → ON CONFLICT DO NOTHING (不报错)
	if err := s.AddLineageEdge(ctx, AddLineageEdgeArgs{
		ChildSHA: "B", ParentSHA: "A", Op: "remix",
	}); err != nil {
		t.Fatal(err)
	}

	// B 的父: A
	parents, err := s.ListParentEdges(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 1 || parents[0].ParentSHA != "A" {
		t.Fatalf("B parents = %+v", parents)
	}

	// A 的子: B
	children, err := s.ListChildEdges(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].ChildSHA != "B" {
		t.Fatalf("A children = %+v", children)
	}
}

func TestLineage_HasAncestor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 链: A → B → C → D
	for _, e := range []struct{ child, parent string }{
		{"B", "A"}, {"C", "B"}, {"D", "C"},
	} {
		if err := s.AddLineageEdge(ctx, AddLineageEdgeArgs{
			ChildSHA: e.child, ParentSHA: e.parent, Op: "remix",
		}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		sha, ancestor string
		want          bool
	}{
		{"D", "A", true}, // 隔三跳
		{"D", "B", true},
		{"D", "C", true},
		{"D", "D", true},  // 自己
		{"A", "D", false}, // 反向
		{"B", "X", false}, // 不存在
	}
	for _, c := range cases {
		got, err := s.HasAncestor(ctx, c.sha, c.ancestor)
		if err != nil {
			t.Fatalf("HasAncestor(%s, %s): %v", c.sha, c.ancestor, err)
		}
		if got != c.want {
			t.Errorf("HasAncestor(%s, %s) = %v, want %v", c.sha, c.ancestor, got, c.want)
		}
	}
}

// ════════════════════════════════════════════════════════════
// Characters
// ════════════════════════════════════════════════════════════

func TestCreateAndDeleteCharacter(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	ctx := context.Background()

	c, err := s.CreateCharacter(ctx, CreateCharacterArgs{
		UserID: &uid, Name: "小琳",
		AvatarURL:    "cas:avatar-sha",
		VoiceDefault: "voice-zh-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "小琳" {
		t.Errorf("name = %q", c.Name)
	}

	got, err := s.GetCharacter(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *got.UserID != uid {
		t.Errorf("user_id mismatch")
	}

	// 删自己的: ok
	if err := s.DeleteCharacter(ctx, uid, c.ID); err != nil {
		t.Fatal(err)
	}
	// 二次删: NotFound
	if err := s.DeleteCharacter(ctx, uid, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-delete: want NotFound, got %v", err)
	}
}

func TestListCharacters_PublicAndOwn(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	ctx := context.Background()

	// 自己创 1 个
	_, _ = s.CreateCharacter(ctx, CreateCharacterArgs{UserID: &uid, Name: "我的"})
	// 别人创 1 个 public
	other := newUser()
	resetUser(t, s, other)
	_, _ = s.CreateCharacter(ctx, CreateCharacterArgs{
		UserID: &other, Name: "他的-public", IsPublic: true,
	})
	// 别人创 1 个 private
	_, _ = s.CreateCharacter(ctx, CreateCharacterArgs{
		UserID: &other, Name: "他的-private", IsPublic: false,
	})

	// 仅自己
	mine, _ := s.ListCharacters(ctx, ListCharactersArgs{UserID: &uid})
	if len(mine) != 1 {
		t.Errorf("mine = %d, want 1", len(mine))
	}
	// 自己 + 公开
	withPub, _ := s.ListCharacters(ctx, ListCharactersArgs{UserID: &uid, IncludePublic: true})
	if len(withPub) < 2 {
		t.Errorf("with public = %d, want >= 2", len(withPub))
	}
}

// ════════════════════════════════════════════════════════════
// Gallery
// ════════════════════════════════════════════════════════════

func TestListGallery_PublicCompletedOnly(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	prov, model := ensureSeedProviderModel(t, s)
	ctx := context.Background()

	// 1 个公开 completed
	t1, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "公开柯基", CostCredits: 40, IsPublic: true,
	})
	now := time.Now()
	prog := int16(100)
	_ = s.UpdateTaskStatus(ctx, UpdateTaskStatusArgs{
		ID: t1.ID, Status: "completed", Progress: &prog, CompletedAt: &now,
	})
	// 1 个私有 completed
	t2, _ := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "私有柯基", CostCredits: 40, IsPublic: false,
	})
	_ = s.UpdateTaskStatus(ctx, UpdateTaskStatusArgs{
		ID: t2.ID, Status: "completed", Progress: &prog, CompletedAt: &now,
	})
	// 1 个公开但 pending (不该出现在 gallery)
	_, _ = s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "进行中", CostCredits: 40, IsPublic: true,
	})

	items, err := s.ListGallery(ctx, ListGalleryArgs{Type: "image"})
	if err != nil {
		t.Fatal(err)
	}
	// 至少包含公开 completed 的; 不能包含私有或 pending 的
	foundPublic := false
	for _, it := range items {
		if it.ID == t1.ID {
			foundPublic = true
		}
		if it.ID == t2.ID {
			t.Errorf("gallery includes private task")
		}
		if it.Status != "completed" || !it.IsPublic {
			t.Errorf("gallery item not public+completed: %+v", it)
		}
	}
	if !foundPublic {
		t.Errorf("public completed task missing from gallery")
	}

	// 关键词 (ILIKE)
	kw, _ := s.ListGallery(ctx, ListGalleryArgs{Keyword: "公开"})
	if len(kw) == 0 {
		t.Errorf("keyword search returned 0")
	}
}
