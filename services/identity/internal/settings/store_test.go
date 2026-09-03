package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 复用 dev 栈 postgres (localhost:15432 转发). 设 SETTINGS_TEST_DATABASE_URL
// 覆盖; 设为 "skip" 跳过整组.
func dbURL() string {
	if v := os.Getenv("SETTINGS_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biumind?sslmode=disable"
}

func newTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := dbURL()
	if url == "skip" {
		t.Skip("SETTINGS_TEST_DATABASE_URL=skip")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool), pool
}

// user_settings 有 FK 到 identity.users — 测试前先建真用户.
func newTestUserInDB(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	email := "settings-test-" + uid.String()[:8] + "@test.local"
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

// assertJSONEq — jsonb 读回会做空白/键序归一化 (如 `{"a": 1}`), 按语义比较.
func assertJSONEq(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("json = %s, want %s", gb, wb)
	}
}

func TestStore_GenericKV(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	// 未设置 → ErrNotFound
	if _, err := s.Get(ctx, uid, "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unset: err = %v, want ErrNotFound", err)
	}

	// Set → Get 往返
	if err := s.Set(ctx, uid, "k1", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.Get(ctx, uid, "k1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertJSONEq(t, got, `{"a":1}`)

	// upsert 覆盖
	if err := s.Set(ctx, uid, "k1", json.RawMessage(`{"a":2}`)); err != nil {
		t.Fatalf("set overwrite: %v", err)
	}
	got, err = s.Get(ctx, uid, "k1")
	if err != nil {
		t.Fatalf("get after overwrite: %v", err)
	}
	assertJSONEq(t, got, `{"a":2}`)

	// key 隔离: 另一 key 不受覆盖影响
	if err := s.Set(ctx, uid, "k2", json.RawMessage(`{"b":true}`)); err != nil {
		t.Fatalf("set k2: %v", err)
	}
	got, err = s.Get(ctx, uid, "k1")
	if err != nil {
		t.Fatalf("k1 after k2 set: err=%v", err)
	}
	assertJSONEq(t, got, `{"a":2}`)

	// Delete → ErrNotFound; 再 Delete 幂等不报错
	if err := s.Delete(ctx, uid, "k1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, uid, "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, uid, "k1"); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
}

func TestStore_IngestModel(t *testing.T) {
	s, pool := newTestStore(t)
	uid := newTestUserInDB(t, pool)
	ctx := context.Background()

	// 未设置 → ErrNotFound
	if _, err := s.GetIngestModel(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unset: err = %v, want ErrNotFound", err)
	}

	if err := s.SetIngestModel(ctx, uid, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.GetIngestModel(ctx, uid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "claude-sonnet-4-6" {
		t.Fatalf("model = %q", got)
	}

	// 底层 jsonb 形态契约: {"model":"..."} — worker / 其他语言消费方依赖它
	raw, err := s.Get(ctx, uid, KeyIngestModel)
	if err != nil {
		t.Fatalf("raw get: %v", err)
	}
	assertJSONEq(t, raw, `{"model":"claude-sonnet-4-6"}`)

	// 覆盖
	if err := s.SetIngestModel(ctx, uid, "gpt-4o"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, _ = s.GetIngestModel(ctx, uid)
	if got != "gpt-4o" {
		t.Fatalf("model after overwrite = %q", got)
	}

	// 清除 → ErrNotFound, 幂等
	if err := s.DeleteIngestModel(ctx, uid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetIngestModel(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}

	// 脏数据 (value 无 model 字段) 按未设置处理
	if err := s.Set(ctx, uid, KeyIngestModel, json.RawMessage(`{"other":1}`)); err != nil {
		t.Fatalf("set dirty: %v", err)
	}
	if _, err := s.GetIngestModel(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dirty value: err = %v, want ErrNotFound", err)
	}
}
