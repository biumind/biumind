// persistRegenerateAndAssemble 集成测试 —— 真 Postgres(DATABASE_URL 缺则
// skip,同 store_test.go 惯例)。验 regenerate 原子重滚:截断 pivot 之后、
// 不新增 user 行、历史以 pivot 为界、参数校验映射 errBadFromMessage。

package agentplane

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
)

func regenHarness(t *testing.T) (*Server, *chatpkg.Store) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	cs := chatpkg.New(pool)
	return &Server{ChatStore: cs}, cs
}

func TestPersistRegenerateAndAssemble(t *testing.T) {
	srv, cs := regenHarness(t)
	ctx := context.Background()
	uid := uuid.New()

	thread, err := cs.CreateThread(ctx, chatpkg.CreateThreadInput{
		UserID: uid, Title: "regen", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	defer cs.DeleteThread(ctx, uid, thread.ID)

	// 两轮对话:user1 → asst1 → user2(pivot) → asst2。
	mk := func(role, content string) *chatpkg.Message {
		m, err := cs.CreateMessage(ctx, chatpkg.CreateMessageInput{
			ThreadID: thread.ID, UserID: uid, Role: role, Content: content,
		})
		if err != nil {
			t.Fatalf("mk %s: %v", role, err)
		}
		return m
	}
	mk(chatpkg.RoleUser, "第一问")
	mk(chatpkg.RoleAssistant, "第一答")
	pivot := mk(chatpkg.RoleUser, "第二问")
	asst2 := mk(chatpkg.RoleAssistant, "第二答(将被截断)")

	history, err := srv.persistRegenerateAndAssemble(
		ctx, uuid.New(), uid, thread.ID, pivot.ID, "test-model", nil)
	if err != nil {
		t.Fatalf("regen: %v", err)
	}

	// 历史 = pivot 之前的两轮,不含 pivot 自身(由 ChatRunner 当 Prompt 发)。
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2: %+v", len(history), history)
	}
	if history[0].Role != "user" || history[0].Content != "第一问" ||
		history[1].Role != "assistant" || history[1].Content != "第一答" {
		t.Errorf("history mismatch: %+v", history)
	}

	// pivot user 行保留,asst2 被截断 —— 库里剩 3 行,user 行数不变。
	remain, err := cs.ListMessages(ctx, chatpkg.ListMessagesInput{
		ThreadID: thread.ID, UserID: uid, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remain) != 3 {
		t.Fatalf("remain len = %d, want 3", len(remain))
	}
	var userRows int
	for _, m := range remain {
		if m.ID == asst2.ID {
			t.Errorf("asst2 should be trimmed")
		}
		if m.Role == chatpkg.RoleUser {
			userRows++
		}
	}
	if userRows != 2 {
		t.Errorf("user rows = %d, want 2 (regenerate 不得新增 user 行)", userRows)
	}

	// 幂等:对同一 pivot 再重滚一次,不再删任何东西,历史不变。
	history2, err := srv.persistRegenerateAndAssemble(
		ctx, uuid.New(), uid, thread.ID, pivot.ID, "test-model", nil)
	if err != nil || len(history2) != 2 {
		t.Errorf("re-regen: len=%d err=%v", len(history2), err)
	}
}

func TestPersistRegenerateAndAssemble_BadPivot(t *testing.T) {
	srv, cs := regenHarness(t)
	ctx := context.Background()
	uid := uuid.New()

	thread, _ := cs.CreateThread(ctx, chatpkg.CreateThreadInput{
		UserID: uid, Title: "regen-bad", SyncEnabled: true,
	})
	defer cs.DeleteThread(ctx, uid, thread.ID)
	other, _ := cs.CreateThread(ctx, chatpkg.CreateThreadInput{
		UserID: uid, Title: "other", SyncEnabled: true,
	})
	defer cs.DeleteThread(ctx, uid, other.ID)

	asst, err := cs.CreateMessage(ctx, chatpkg.CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: chatpkg.RoleAssistant, Content: "答",
	})
	if err != nil {
		t.Fatalf("asst: %v", err)
	}
	userInOther, err := cs.CreateMessage(ctx, chatpkg.CreateMessageInput{
		ThreadID: other.ID, UserID: uid,
		Role: chatpkg.RoleUser, Content: "别的 thread 的问",
	})
	if err != nil {
		t.Fatalf("userInOther: %v", err)
	}

	cases := map[string]uuid.UUID{
		"不存在":        uuid.New(),
		"非 user 消息":  asst.ID,
		"不属该 thread": userInOther.ID,
	}
	for name, pivotID := range cases {
		_, err := srv.persistRegenerateAndAssemble(
			ctx, uuid.New(), uid, thread.ID, pivotID, "m", nil)
		if !errors.Is(err, errBadFromMessage) {
			t.Errorf("%s: err = %v, want errBadFromMessage", name, err)
		}
	}

	// 跨用户:pivot 属于 uid,用别人的 uid 调 → GetMessage 命中不到 → 400。
	_, err = srv.persistRegenerateAndAssemble(
		ctx, uuid.New(), uuid.New(), thread.ID, asst.ID, "m", nil)
	if !errors.Is(err, errBadFromMessage) {
		t.Errorf("跨用户: err = %v, want errBadFromMessage", err)
	}
}
