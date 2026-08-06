package byok

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 复用 dev biu_core. 设 BYOK_TEST_DATABASE_URL=skip 跳过.
func dbURL() string {
	if v := os.Getenv("BYOK_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := dbURL()
	if url == "skip" {
		t.Skip("BYOK_TEST_DATABASE_URL=skip")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG ping: %v", err)
	}
	t.Cleanup(pool.Close)
	c := newTestCipher(t)
	return NewStore(pool, c), pool
}

// 我们写测试用 user 不能 FK 到 identity.users — 需先建一个真用户.
func newTestUserInDB(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	email := "byok-test-" + uid.String()[:8] + "@test.local"
	_, err := pool.Exec(context.Background(),
		`INSERT INTO identity.users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		uid, email)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM identity.users WHERE id = $1`, uid)
	})
	return uid
}

func TestStore_Upsert_AndGetPublic(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	e, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "anthropic",
		Plaintext: "sk-ant-api03-abcdefghijklmnop", Label: "主号",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if e.Provider != "anthropic" || e.Label != "主号" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Last4 != "mnop" {
		t.Fatalf("last4 = %q (want mnop)", e.Last4)
	}
	if e.Status != StatusValid {
		t.Fatalf("status = %s", e.Status)
	}

	// 再读应一致
	got, err := s.GetPublic(ctx, uid, "anthropic")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != e.ID || got.Last4 != "mnop" {
		t.Fatalf("get got %+v", got)
	}
}

func TestStore_Upsert_OverwritesExisting(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	e1, _ := s.Upsert(ctx, UpsertArgs{UserID: uid, Provider: "openai", Plaintext: "sk-aaaaa1234"})
	e2, err := s.Upsert(ctx, UpsertArgs{UserID: uid, Provider: "openai", Plaintext: "sk-bbbbb5678"})
	if err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	// 同一 row id (UPDATE 而非 INSERT)
	if e1.ID != e2.ID {
		t.Fatalf("id changed (should overwrite same row): %s → %s", e1.ID, e2.ID)
	}
	if e2.Last4 != "5678" {
		t.Fatalf("last4 = %q", e2.Last4)
	}
}

func TestStore_GetDecrypted_HappyPath(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	plaintext := "sk-deepseek-secret-12345"
	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "deepseek", Plaintext: plaintext,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, _, _, _, err := s.GetDecrypted(ctx, uid, "deepseek")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("decrypted = %q, want %q", got, plaintext)
	}
}

func TestStore_GetDecrypted_RevokedReturnsNotFound(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "sk-x123",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.Revoke(ctx, uid, "openai", false, nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, _, _, _, err := s.GetDecrypted(ctx, uid, "openai")
	if err != ErrKeyNotFound {
		t.Fatalf("want ErrKeyNotFound, got %v", err)
	}
}

func TestStore_ListPublic_OrderedByProvider(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	for _, p := range []string{"openai", "anthropic", "deepseek"} {
		if _, err := s.Upsert(ctx, UpsertArgs{
			UserID: uid, Provider: p, Plaintext: "sk-" + p,
		}); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}
	list, err := s.ListPublic(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len=%d", len(list))
	}
	want := []string{"anthropic", "deepseek", "openai"}
	for i, e := range list {
		if e.Provider != want[i] {
			t.Fatalf("idx %d = %s, want %s", i, e.Provider, want[i])
		}
	}
}

func TestStore_IncrementFailure_AutoInvalid(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "sk-test",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 4 次不应触发 auto-invalid
	for i := 1; i <= 4; i++ {
		auto, err := s.IncrementFailure(ctx, uid, "openai")
		if err != nil {
			t.Fatalf("inc %d: %v", i, err)
		}
		if auto {
			t.Fatalf("auto-invalid too early at i=%d", i)
		}
	}
	// 第 5 次触发
	auto, err := s.IncrementFailure(ctx, uid, "openai")
	if err != nil {
		t.Fatalf("inc 5: %v", err)
	}
	if !auto {
		t.Fatal("should auto-invalid at 5")
	}

	got, _ := s.GetPublic(ctx, uid, "openai")
	if got.Status != StatusInvalid {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestStore_MarkValidated(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "anthropic", Plaintext: "sk-x",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 故意标记为 invalid
	if err := s.MarkValidated(ctx, uid, "anthropic", false); err != nil {
		t.Fatalf("mark invalid: %v", err)
	}
	got, _ := s.GetPublic(ctx, uid, "anthropic")
	if got.Status != StatusInvalid {
		t.Fatalf("status = %s", got.Status)
	}
	if got.LastValidatedAt == nil {
		t.Fatal("last_validated_at should be set")
	}

	// 再次校验为 valid 重置 failure_count
	if _, err := s.IncrementFailure(ctx, uid, "anthropic"); err != nil {
		t.Fatalf("inc: %v", err)
	}
	if err := s.MarkValidated(ctx, uid, "anthropic", true); err != nil {
		t.Fatalf("mark valid: %v", err)
	}
	got2, _ := s.GetPublic(ctx, uid, "anthropic")
	if got2.Status != StatusValid {
		t.Fatalf("status = %s", got2.Status)
	}
	if got2.FailureCount != 0 {
		t.Fatalf("failure_count = %d (want 0)", got2.FailureCount)
	}
}

func TestStore_Upsert_RejectsBadProvider(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	_, err := s.Upsert(context.Background(), UpsertArgs{
		UserID: uid, Provider: "evilcorp", Plaintext: "sk-x",
	})
	if err != ErrInvalidProvider {
		t.Fatalf("want ErrInvalidProvider, got %v", err)
	}
}

func TestStore_Upsert_RejectsEmpty(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	_, err := s.Upsert(context.Background(), UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "",
	})
	if err != ErrEmptyPlaintext {
		t.Fatalf("want ErrEmptyPlaintext, got %v", err)
	}
}

// last4 边界
func TestStore_Last4_AlwaysWritten(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	cases := []struct{ in, want string }{
		{"abcd", "abcd"},
		{"sk-1234567890abcdef", "cdef"},
		{"x", "x"},
	}
	ctx := context.Background()
	for _, c := range cases {
		e, err := s.Upsert(ctx, UpsertArgs{
			UserID: uid, Provider: "openai", Plaintext: c.in,
		})
		if err != nil {
			t.Fatalf("upsert %q: %v", c.in, err)
		}
		if !strings.HasSuffix(c.in, e.Last4) {
			t.Errorf("Last4(%q) = %q (want suffix)", c.in, e.Last4)
		}
		if e.Last4 != c.want {
			t.Errorf("Last4(%q) = %q, want %q", c.in, e.Last4, c.want)
		}
	}
}

// ─── 00033: custom provider ───────────────────────────

func TestStore_Upsert_CustomRequiresBaseURL(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	_, err := s.Upsert(context.Background(), UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-x",
		// BaseURL 空 → 应被拒
	})
	if err != ErrCustomRequiresEndpoint {
		t.Fatalf("want ErrCustomRequiresEndpoint, got %v", err)
	}
}

func TestStore_Upsert_CustomMultipleBaseURL(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()
	// 同 user 两个 custom (不同 base_url) 应并存 —— partial unique by base_url
	e1, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-aaaa1111",
		BaseURL: "https://proxy-a.example.com", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	})
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if e1.BaseURL != "https://proxy-a.example.com" || e1.Protocol != "openai_compat" {
		t.Fatalf("entry1 = %+v", e1)
	}
	e2, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-bbbb2222",
		BaseURL: "https://proxy-b.example.com", Protocol: "openai_compat",
		ModelGlobs: []string{"deepseek-*"},
	})
	if err != nil {
		t.Fatalf("upsert 2 (不同 base_url 应并存): %v", err)
	}
	if e1.ID == e2.ID {
		t.Fatal("两条 custom 应是不同 row")
	}
}

func TestStore_Upsert_CustomDuplicateBaseURL(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()
	_, _ = s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-aaaa1111",
		BaseURL: "https://proxy-a.example.com", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	})
	// 同 base_url 第二条 → ON CONFLICT 覆盖 (partial unique (user_id, base_url))
	e2, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-bbbb2222",
		BaseURL: "https://proxy-a.example.com", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	})
	if err != nil {
		t.Fatalf("upsert same base_url (应覆盖): %v", err)
	}
	if e2.Last4 != "2222" {
		t.Fatalf("覆盖后 last4 = %q (want 2222)", e2.Last4)
	}
}

// ─── 00034: model_globs 匹配 ──────────────────────────

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		globs []string
		model string
		want  bool
	}{
		{[]string{"*"}, "anything", true},
		{[]string{"glm-*"}, "glm-4.5", true},
		{[]string{"glm-*"}, "gpt-4o", false},
		{[]string{"glm-4.5"}, "glm-4.5", true},
		{[]string{"glm-4.5"}, "glm-4.6", false},
		{[]string{"glm-*", "deepseek-*"}, "deepseek-chat", true},
		{[]string{}, "anything", false},
		{[]string{"glm-*"}, "", false},
	}
	for _, c := range cases {
		if got := globMatch(c.globs, c.model); got != c.want {
			t.Errorf("globMatch(%v, %q) = %v, want %v", c.globs, c.model, got, c.want)
		}
	}
}

func TestStore_MatchCustomByModel(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()
	// 两个 custom: glm-* → proxy-a, deepseek-* → proxy-b
	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-glm-key",
		BaseURL: "https://proxy-a.example.com", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	}); err != nil {
		t.Fatalf("upsert glm: %v", err)
	}
	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", Plaintext: "sk-ds-key",
		BaseURL: "https://proxy-b.example.com", Protocol: "openai_compat",
		ModelGlobs: []string{"deepseek-*"},
	}); err != nil {
		t.Fatalf("upsert deepseek: %v", err)
	}
	// glm-4.5 → proxy-a key
	pt, _, baseURL, protocol, err := s.MatchCustomByModel(ctx, uid, "glm-4.5")
	if err != nil {
		t.Fatalf("match glm-4.5: %v", err)
	}
	if pt != "sk-glm-key" || baseURL != "https://proxy-a.example.com" || protocol != "openai_compat" {
		t.Fatalf("match glm-4.5 = pt=%q base=%q proto=%q", pt, baseURL, protocol)
	}
	// deepseek-chat → proxy-b key
	pt2, _, _, _, err := s.MatchCustomByModel(ctx, uid, "deepseek-chat")
	if err != nil || pt2 != "sk-ds-key" {
		t.Fatalf("match deepseek-chat = %q err=%v (want sk-ds-key)", pt2, err)
	}
	// 无匹配 → ErrKeyNotFound
	_, _, _, _, err = s.MatchCustomByModel(ctx, uid, "claude-opus")
	if err != ErrKeyNotFound {
		t.Fatalf("match claude-opus: want ErrKeyNotFound, got %v", err)
	}
}

// ─── 00035: client-side BYOK (key 仅存客户端, relay 永不解析) ────

// client-side 行: encrypted_value/nonce 空占位, last4 透传. relay 走
// GetDecrypted/GetPublic 必须看不到此类行 (store WHERE 已滤).
func TestStore_Upsert_ClientSideSkipsRelay(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	e, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "anthropic",
		IsClientSide: true, Plaintext: "sk-test-wxyz",
	})
	if err != nil {
		t.Fatalf("upsert client-side: %v", err)
	}
	if !e.IsClientSide {
		t.Fatal("IsClientSide should be true")
	}
	if e.Last4 != "wxyz" {
		t.Fatalf("last4 = %q (want wxyz, 服务端从明文算)", e.Last4)
	}

	// relay 路径 GetDecrypted 必须看不到 client-side 行
	_, _, _, _, err = s.GetDecrypted(ctx, uid, "anthropic")
	if err != ErrKeyNotFound {
		t.Fatalf("GetDecrypted on client-side: want ErrKeyNotFound, got %v", err)
	}
	// GetPublic 只查 server 行, 也应 miss
	_, err = s.GetPublic(ctx, uid, "anthropic")
	if err != ErrKeyNotFound {
		t.Fatalf("GetPublic on client-side: want ErrKeyNotFound, got %v", err)
	}
	// ListPublic 应能看到元数据 (区分两区用)
	list, err := s.ListPublic(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || !list[0].IsClientSide {
		t.Fatalf("list should contain the client-side entry, got %+v", list)
	}
}

// MatchCustomByModel (relay 按 model 匹配 custom) 必须跳过 client-side custom 行.
func TestStore_MatchCustom_SkipsClientSide(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", IsClientSide: true, Plaintext: "sk-test-1234",
		BaseURL: "https://internal-vllm/v1", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	}); err != nil {
		t.Fatalf("upsert client-side custom: %v", err)
	}
	// relay 按 model 匹配 custom 必须看不到 client-side 行
	_, _, _, _, err := s.MatchCustomByModel(ctx, uid, "glm-4.5")
	if err != ErrKeyNotFound {
		t.Fatalf("MatchCustomByModel on client-side: want ErrKeyNotFound, got %v", err)
	}
}

// 方案 I: 同 (user, provider) 可 server + client-side 两行, 不撞 unique.
// 各自覆盖幂等 (按 is_client_side 命中各自 partial unique index).
func TestStore_Upsert_ServerAndClientSideCoexist(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	server, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "sk-server1234",
	})
	if err != nil {
		t.Fatalf("upsert server: %v", err)
	}
	if server.IsClientSide {
		t.Fatal("server should be is_client_side=false")
	}
	client, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", IsClientSide: true, Plaintext: "sk-cli-cdef",
	})
	if err != nil {
		t.Fatalf("upsert client-side same provider (应并存不撞 unique): %v", err)
	}
	if client.ID == server.ID {
		t.Fatal("server 和 client-side 应是不同 row")
	}
	if !client.IsClientSide {
		t.Fatal("client should be is_client_side=true")
	}

	// 各自覆盖幂等 (命中同 row)
	server2, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "sk-server5678",
	})
	if err != nil || server2.ID != server.ID {
		t.Fatalf("server 覆盖应命中同 row: err=%v id=%s", err, server2.ID)
	}
	client2, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", IsClientSide: true, Plaintext: "sk-cli-9999",
	})
	if err != nil || client2.ID != client.ID {
		t.Fatalf("client-side 覆盖应命中同 row: err=%v id=%s", err, client2.ID)
	}

	// GetDecrypted 只见 server 行的最新 key
	pt, _, _, _, err := s.GetDecrypted(ctx, uid, "openai")
	if err != nil || pt != "sk-server5678" {
		t.Fatalf("GetDecrypted = pt=%q err=%v (want sk-server5678)", pt, err)
	}

	// ListPublic 见两行 (server + client)
	list, _ := s.ListPublic(ctx, uid)
	if len(list) != 2 {
		t.Fatalf("list len = %d (want 2: server + client)", len(list))
	}
}

// custom client-side 多 base_url: 同 provider 多行. Revoke 传 id 精确删单条,
// 不误伤其余 (旧行为按 provider+client_side 批删会清光全部 custom client 行).
func TestStore_Revoke_CustomClientSide_ByID(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	e1, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", IsClientSide: true, Plaintext: "sk-rvk-1111",
		BaseURL: "https://vllm-a/v1", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	})
	if err != nil {
		t.Fatalf("upsert custom client 1: %v", err)
	}
	e2, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "custom", IsClientSide: true, Plaintext: "sk-rvk-2222",
		BaseURL: "https://vllm-b/v1", Protocol: "openai_compat",
		ModelGlobs: []string{"glm-*"},
	})
	if err != nil {
		t.Fatalf("upsert custom client 2: %v", err)
	}
	if e1.ID == e2.ID {
		t.Fatal("两 custom client 行应按 base_url 区分成不同 row")
	}

	// 按 e1.ID 精确 revoke
	if err := s.Revoke(ctx, uid, "custom", true, &e1.ID); err != nil {
		t.Fatalf("revoke by id: %v", err)
	}

	list, err := s.ListPublic(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var e1Status, e2Status string
	for _, e := range list {
		if e.ID == e1.ID {
			e1Status = string(e.Status)
		}
		if e.ID == e2.ID {
			e2Status = string(e.Status)
		}
	}
	if e1Status != "revoked" {
		t.Fatalf("e1 应被 revoke, status=%q", e1Status)
	}
	if e2Status != "valid" {
		t.Fatalf("e2 应存活 (valid), status=%q (按 id 删不该误伤)", e2Status)
	}
}

// ─── 00036: 编辑不改 key + GetDecryptedByID ───────────

// 编辑 (Plaintext 空) 保留原加密值, 不改 key. 新建 Plaintext 空仍报错.
func TestStore_Upsert_EditKeepsKey(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	// 新建必须 key
	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "",
	}); err != ErrEmptyPlaintext {
		t.Fatalf("新建 Plaintext 空: want ErrEmptyPlaintext, got %v", err)
	}

	// 新建 key A
	if _, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "sk-aaaa1111",
	}); err != nil {
		t.Fatalf("upsert A: %v", err)
	}

	// 编辑 label 不改 key (Plaintext 空)
	e2, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "", Label: "新标签",
	})
	if err != nil {
		t.Fatalf("upsert edit: %v", err)
	}
	if e2.Label != "新标签" {
		t.Fatalf("label = %q", e2.Label)
	}
	if e2.Last4 != "1111" {
		t.Fatalf("last4 = %q (want 1111, 编辑不改 key)", e2.Last4)
	}

	// GetDecrypted 仍返原 key A
	pt, _, _, _, err := s.GetDecrypted(ctx, uid, "openai")
	if err != nil || pt != "sk-aaaa1111" {
		t.Fatalf("GetDecrypted = pt=%q err=%v (want sk-aaaa1111)", pt, err)
	}
}

// GetDecryptedByID: 仅 is_client_side=true 行可取; server 行 / 跨 user / 非 valid → ErrKeyNotFound.
func TestStore_GetDecryptedByID(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	other := newTestUserInDB(t, pool)
	ctx := context.Background()

	// client-side 行
	client, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "anthropic", IsClientSide: true,
		Plaintext: "sk-cli-secret",
	})
	if err != nil {
		t.Fatalf("upsert client: %v", err)
	}
	// server 行
	server, err := s.Upsert(ctx, UpsertArgs{
		UserID: uid, Provider: "openai", Plaintext: "sk-srv-secret",
	})
	if err != nil {
		t.Fatalf("upsert server: %v", err)
	}

	// client-side 行: 取到明文
	pt, _, _, _, _, err := s.GetDecryptedByID(ctx, uid, client.ID)
	if err != nil {
		t.Fatalf("GetDecryptedByID client: %v", err)
	}
	if pt != "sk-cli-secret" {
		t.Fatalf("pt = %q", pt)
	}

	// server 行: ErrKeyNotFound (仅 client-side 可取)
	_, _, _, _, _, err = s.GetDecryptedByID(ctx, uid, server.ID)
	if err != ErrKeyNotFound {
		t.Fatalf("GetDecryptedByID server: want ErrKeyNotFound, got %v", err)
	}

	// 跨 user: ErrKeyNotFound (owner-scoped)
	_, _, _, _, _, err = s.GetDecryptedByID(ctx, other, client.ID)
	if err != ErrKeyNotFound {
		t.Fatalf("GetDecryptedByID cross-user: want ErrKeyNotFound, got %v", err)
	}

	// revoke 后: ErrKeyNotFound (非 valid)
	if err := s.Revoke(ctx, uid, "anthropic", true, &client.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, _, _, _, _, err = s.GetDecryptedByID(ctx, uid, client.ID)
	if err != ErrKeyNotFound {
		t.Fatalf("GetDecryptedByID revoked: want ErrKeyNotFound, got %v", err)
	}
}
