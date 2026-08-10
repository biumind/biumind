// Integration tests against a real Postgres (docker compose), same
// paradigm as store_test.go — skips when DATABASE_URL is unset.

package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// EnsureThread 归属校验(本地数据隔离设计 §3.4):
//   - 同 user 重复 ensure 同一 id → 幂等成功;
//   - 不同 user ensure 同一 id(同设备换账号残留旧 thread id)
//     → ErrThreadOwnedByOther,绝不静默认领别人的 thread。
func TestEnsureThreadOwnership(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	threadID := uuid.New()
	owner := uuid.New()
	other := uuid.New()

	// First insert creates the thread for owner.
	if err := s.EnsureThread(ctx, threadID, owner, "first prompt", "claude-opus-4-7"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	// Same user again is an idempotent no-op (title/model not overwritten).
	if err := s.EnsureThread(ctx, threadID, owner, "different title", "other-model"); err != nil {
		t.Fatalf("idempotent ensure: %v", err)
	}
	got, err := s.GetThread(ctx, owner, threadID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "first prompt" {
		t.Errorf("title should not be overwritten, got %q", got.Title)
	}

	// Another account reusing the same id must be rejected.
	err = s.EnsureThread(ctx, threadID, other, "hijack attempt", "")
	if !errors.Is(err, ErrThreadOwnedByOther) {
		t.Fatalf("expected ErrThreadOwnedByOther, got %v", err)
	}
	// Ownership unchanged: owner still sees the thread, other does not.
	if _, err := s.GetThread(ctx, owner, threadID); err != nil {
		t.Errorf("owner lost thread: %v", err)
	}
	if _, err := s.GetThread(ctx, other, threadID); err != ErrNotFound {
		t.Errorf("other user should not see thread, got %v", err)
	}

	defer s.DeleteThread(ctx, owner, threadID)
}
