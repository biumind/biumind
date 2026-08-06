// Integration tests against real Postgres. Same skip-on-missing-DB pattern as
// internal/code/store_test.go.

package files

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	return NewStore(pool), pool.Close
}

func mkObj(uid uuid.UUID, sha string) Object {
	mime := "image/png"
	return Object{
		ID:        uuid.New(),
		UserID:    uid,
		Sha256:    sha,
		SizeBytes: 12345,
		MimeType:  &mime,
		Bucket:    "biumind-files-test",
		ObjectKey: uid.String() + "/" + uuid.New().String(),
		Source:    "test",
		Metadata:  json.RawMessage(`{"task_id":"abc"}`),
	}
}

func TestFiles_InsertAndGet(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	obj := mkObj(uid, "sha-A")
	if err := s.Insert(ctx, obj); err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer s.SoftDelete(ctx, uid, obj.ID)

	got, err := s.Get(ctx, uid, obj.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Sha256 != "sha-A" || got.ObjectKey != obj.ObjectKey {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestFiles_LookupBySha256_DedupSameUser(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	first := mkObj(uid, "sha-DEDUP")
	if err := s.Insert(ctx, first); err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer s.SoftDelete(ctx, uid, first.ID)

	got, err := s.LookupBySha256(ctx, uid, "sha-DEDUP")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || got.ID != first.ID {
		t.Errorf("expected dedup hit, got %+v", got)
	}

	// 第二次 Insert 同 (user_id, sha256) — 唯一索引应该 reject。
	second := mkObj(uid, "sha-DEDUP")
	err = s.Insert(ctx, second)
	if err == nil {
		_ = s.SoftDelete(ctx, uid, second.ID)
		t.Errorf("expected unique-violation on duplicate (uid, sha256)")
	}
}

func TestFiles_LookupBySha256_NotCrossUser(t *testing.T) {
	// 同 sha256 跨用户 NOT dedup — 隐私+配额隔离。每个用户独立存储 (object_key
	// 独立, 不共享)。
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	uidA := uuid.New()
	uidB := uuid.New()
	a := mkObj(uidA, "sha-XYZ")
	if err := s.Insert(ctx, a); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	defer s.SoftDelete(ctx, uidA, a.ID)

	// B 查同 sha256 — 不该命中 A 的对象
	got, err := s.LookupBySha256(ctx, uidB, "sha-XYZ")
	if err != nil {
		t.Fatalf("lookup B: %v", err)
	}
	if got != nil {
		t.Errorf("cross-user dedup leaked: %+v", got)
	}
}

func TestFiles_GetCrossTenantNotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner := uuid.New()
	intruder := uuid.New()
	obj := mkObj(owner, "sha-OWN")
	if err := s.Insert(ctx, obj); err != nil {
		t.Fatalf("insert: %v", err)
	}
	defer s.SoftDelete(ctx, owner, obj.ID)

	if _, err := s.Get(ctx, intruder, obj.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant get: expected ErrNotFound, got %v", err)
	}
}

func TestFiles_SoftDeleteIdempotentAndScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	obj := mkObj(uid, "sha-DEL")
	if err := s.Insert(ctx, obj); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.SoftDelete(ctx, uid, obj.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// 第二次 delete 应该 ErrNotFound
	if err := s.SoftDelete(ctx, uid, obj.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: expected ErrNotFound, got %v", err)
	}
	// 跨用户 delete 也是 ErrNotFound
	if err := s.SoftDelete(ctx, uuid.New(), obj.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant delete: expected ErrNotFound, got %v", err)
	}
	// soft delete 后 Get 也找不到
	if _, err := s.Get(ctx, uid, obj.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete: expected ErrNotFound, got %v", err)
	}
	// soft delete 后允许 dedup index 释放 — 同 (uid, sha) 可重新 Insert
	again := mkObj(uid, "sha-DEL")
	if err := s.Insert(ctx, again); err != nil {
		t.Errorf("re-insert after soft delete should succeed: %v", err)
	} else {
		_ = s.SoftDelete(ctx, uid, again.ID)
	}
}

func TestFiles_InsertRejectsZeroFields(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, c := range []struct {
		name string
		mut  func(*Object)
	}{
		{"id zero", func(o *Object) { o.ID = uuid.Nil }},
		{"user zero", func(o *Object) { o.UserID = uuid.Nil }},
		{"sha empty", func(o *Object) { o.Sha256 = "" }},
		{"bucket empty", func(o *Object) { o.Bucket = "" }},
		{"object_key empty", func(o *Object) { o.ObjectKey = "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			obj := mkObj(uuid.New(), "sha-VALID")
			c.mut(&obj)
			if err := s.Insert(ctx, obj); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: expected ErrInvalid, got %v", c.name, err)
			}
		})
	}
}
