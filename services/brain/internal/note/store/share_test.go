// 笔记分享 store 测试 —— 真 Postgres（同 store_test.go 约定，
// DATABASE_URL 未设跳过）。覆盖：PUT 幂等 / 密码设置-重设-移除与
// credential_version / 停用-恢复 / rotate 旧 token 失效 / 列表 join 标题 /
// view_count / 公开端辅助查询 / biu-file:// 改写。

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// cleanupShares —— 物理清掉测试用户的分享行 / 审计事件 / 测试文件对象。
// 笔记由 cleanupNotes 清（note_shares 随 note 硬删 CASCADE，但测试
// 用户可能只有分享没有笔记的场景不存在，双清兜底）。
func (h *storeHarness) cleanupShares(t *testing.T, uid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.note_shares WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup shares: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM brain.events WHERE scope = $1 AND actor_id = $2`,
		ShareEventScope, uid.String()); err != nil {
		t.Fatalf("cleanup share events: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM files.objects WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("cleanup files objects: %v", err)
	}
}

func upsertShare(t *testing.T, h *storeHarness, noteID, uid uuid.UUID, in UpsertShareInput) *Share {
	t.Helper()
	in.NoteID, in.UserID, in.ActorID = noteID, uid, uid.String()
	if in.Token == "" {
		in.Token = "tok-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:28]
	}
	sh, err := h.st.UpsertShare(context.Background(), in)
	if err != nil {
		t.Fatalf("UpsertShare: %v", err)
	}
	return sh
}

func TestShareCreateAndIdempotentUpsert(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	n := createNote(t, h, uid, "分享目标", "正文")

	// 新建：credential_version=1、无密码、永久
	sh := upsertShare(t, h, n.ID, uid, UpsertShareInput{})
	if sh.CredentialVersion != 1 || sh.PasswordHash != nil || sh.ExpiresAt != nil || sh.DisabledAt != nil {
		t.Fatalf("new share fields wrong: %+v", sh)
	}
	if len(sh.Token) < 20 {
		t.Fatalf("token looks wrong: %q", sh.Token)
	}

	// 幂等更新：同一篇重复 PUT → 同 id 同 token，只改配置
	exp := time.Now().Add(7 * 24 * time.Hour)
	sh2 := upsertShare(t, h, n.ID, uid, UpsertShareInput{Token: "ignored-on-update", ExpiresSet: true, ExpiresAt: &exp})
	if sh2.ID != sh.ID || sh2.Token != sh.Token {
		t.Fatalf("upsert should keep id/token: %+v vs %+v", sh2, sh)
	}
	if sh2.ExpiresAt == nil {
		t.Fatalf("expires_at not updated: %+v", sh2)
	}

	// 设置密码（expires_in 缺省）→ credential_version+1 且 expires_at 原值不动
	hash := "$2a$10$fakehashfakehashfakehashfakehashfakehashfakehashf"
	sh3 := upsertShare(t, h, n.ID, uid, UpsertShareInput{PasswordSet: true, PasswordHash: &hash})
	if sh3.CredentialVersion != 2 || sh3.PasswordHash == nil || *sh3.PasswordHash != hash {
		t.Fatalf("password set: cv=%d hash=%v", sh3.CredentialVersion, sh3.PasswordHash)
	}
	if sh3.ExpiresAt == nil || sh3.ExpiresAt.Unix() != exp.Unix() {
		t.Fatalf("expires_at should be kept when expires_in omitted: %v want ~%v", sh3.ExpiresAt, exp)
	}

	// 密码缺省 = 保持不变
	sh4 := upsertShare(t, h, n.ID, uid, UpsertShareInput{})
	if sh4.CredentialVersion != 2 || sh4.PasswordHash == nil {
		t.Fatalf("password should stay: cv=%d hash=%v", sh4.CredentialVersion, sh4.PasswordHash)
	}

	// "" = 移除密码（契约：只有「重设」才 +1，移除不动 credential_version——
	// 移除后校验链②整步跳过，旧 JWT 自然失效）
	sh5 := upsertShare(t, h, n.ID, uid, UpsertShareInput{PasswordSet: true, PasswordHash: nil})
	if sh5.PasswordHash != nil || sh5.CredentialVersion != 2 {
		t.Fatalf("password remove: cv=%d hash=%v", sh5.CredentialVersion, sh5.PasswordHash)
	}
}

// TestShareExpiresTriState —— expires_in 三态（契约修订：允许缺省）：
// 更新缺省 = 保持原值；显式 never = 置 NULL；停用恢复缺省 = 保持原值。
func TestShareExpiresTriState(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	ctx := context.Background()
	n := createNote(t, h, uid, "有效期三态", "正文")

	// 新建缺省 → never
	sh := upsertShare(t, h, n.ID, uid, UpsertShareInput{})
	if sh.ExpiresAt != nil {
		t.Fatalf("create with omitted expires_in should be never: %+v", sh)
	}
	// 显式设 7d → 更新缺省 → 原值不动
	exp := time.Now().Add(7 * 24 * time.Hour)
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{ExpiresSet: true, ExpiresAt: &exp})
	if sh.ExpiresAt == nil {
		t.Fatalf("expires_at not set: %+v", sh)
	}
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{})
	if sh.ExpiresAt == nil || sh.ExpiresAt.Unix() != exp.Unix() {
		t.Fatalf("omitted expires_in should keep value: %v", sh.ExpiresAt)
	}
	// 显式 never → NULL
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{ExpiresSet: true, ExpiresAt: nil})
	if sh.ExpiresAt != nil {
		t.Fatalf("explicit never should null expires_at: %+v", sh)
	}
	// 停用后恢复缺省 → 保持原 expires_at（先设回 7d 再停用）
	exp2 := time.Now().Add(7 * 24 * time.Hour)
	upsertShare(t, h, n.ID, uid, UpsertShareInput{ExpiresSet: true, ExpiresAt: &exp2})
	if err := h.st.DisableShare(ctx, n.ID, uid, uid.String()); err != nil {
		t.Fatalf("DisableShare: %v", err)
	}
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{Token: "must-not-be-used"})
	if sh.DisabledAt != nil {
		t.Fatalf("restore should clear disabled_at: %+v", sh)
	}
	if sh.ExpiresAt == nil || sh.ExpiresAt.Unix() != exp2.Unix() {
		t.Fatalf("restore with omitted expires_in should keep original: %v", sh.ExpiresAt)
	}
}

func TestShareDisableRestoreRotate(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	n := createNote(t, h, uid, "停用恢复", "正文")
	ctx := context.Background()

	sh := upsertShare(t, h, n.ID, uid, UpsertShareInput{})

	// 停用 → 再停用 404
	if err := h.st.DisableShare(ctx, n.ID, uid, uid.String()); err != nil {
		t.Fatalf("DisableShare: %v", err)
	}
	if err := h.st.DisableShare(ctx, n.ID, uid, uid.String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double disable should be ErrNotFound, got %v", err)
	}
	got, err := h.st.GetShareByToken(ctx, sh.Token)
	if err != nil || got.DisabledAt == nil {
		t.Fatalf("disabled share row: %+v err=%v", got, err)
	}

	// 对已停用分享 PUT = 以原 token 恢复
	sh2 := upsertShare(t, h, n.ID, uid, UpsertShareInput{Token: "must-not-be-used"})
	if sh2.Token != sh.Token || sh2.DisabledAt != nil || sh2.ID != sh.ID {
		t.Fatalf("restore should keep original token: %+v", sh2)
	}

	// rotate → 新 token + credential_version+1；旧 token 即 404
	newTok := "rotated-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]
	sh3, err := h.st.RotateShare(ctx, n.ID, uid, newTok, uid.String())
	if err != nil {
		t.Fatalf("RotateShare: %v", err)
	}
	if sh3.Token != newTok || sh3.CredentialVersion != sh2.CredentialVersion+1 {
		t.Fatalf("rotated share: %+v", sh3)
	}
	if _, err := h.st.GetShareByToken(ctx, sh.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token should be gone, got %v", err)
	}
	if _, err := h.st.GetShareByToken(ctx, newTok); err != nil {
		t.Fatalf("new token should resolve: %v", err)
	}

	// 无分享行的笔记 rotate → ErrNotFound
	n2 := createNote(t, h, uid, "无分享", "")
	if _, err := h.st.RotateShare(ctx, n2.ID, uid, "x", uid.String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate without share should be ErrNotFound, got %v", err)
	}
}

func TestShareListAndViewCount(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	ctx := context.Background()

	n1 := createNote(t, h, uid, "第一篇", "")
	n2 := createNote(t, h, uid, "第二篇", "")
	sh1 := upsertShare(t, h, n1.ID, uid, UpsertShareInput{})
	upsertShare(t, h, n2.ID, uid, UpsertShareInput{})
	if err := h.st.DisableShare(ctx, n1.ID, uid, uid.String()); err != nil {
		t.Fatalf("DisableShare: %v", err)
	}

	list, err := h.st.ListShares(ctx, uid)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 shares, got %d", len(list))
	}
	titles := map[uuid.UUID]string{}
	for _, sw := range list {
		titles[sw.NoteID] = sw.NoteTitle
	}
	if titles[n1.ID] != "第一篇" || titles[n2.ID] != "第二篇" {
		t.Fatalf("joined titles wrong: %v", titles)
	}

	// view_count 自增（无 session = 每次照计）
	for i := 0; i < 3; i++ {
		counted, err := h.st.RecordShareView(ctx, sh1.ID, nil)
		if err != nil || !counted {
			t.Fatalf("RecordShareView: counted=%v err=%v", counted, err)
		}
	}
	got, err := h.st.GetShareByToken(ctx, sh1.Token)
	if err != nil {
		t.Fatalf("GetShareByToken: %v", err)
	}
	if got.ViewCount != 3 {
		t.Fatalf("view_count: got %d want 3", got.ViewCount)
	}

	// 审计事件确实落了 note_share scope（创建×2 + 停用×1）
	var evCount int
	if err := h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM brain.events WHERE scope = $1 AND actor_id = $2
	`, ShareEventScope, uid.String()).Scan(&evCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if evCount < 3 {
		t.Fatalf("expected >=3 audit events, got %d", evCount)
	}
}

func TestSharePublicHelpers(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	ctx := context.Background()

	fileID := uuid.New()
	content := fmt.Sprintf("看图 ![x](biu-file://%s) 以及文本里提到的 biu-file:// 字样。", fileID)
	n := createNote(t, h, uid, "附件笔记", content)

	// GetPublicNote：无 user 过滤；软删后 DeletedAt 非空
	pn, err := h.st.GetPublicNote(ctx, n.ID)
	if err != nil || pn.DeletedAt != nil {
		t.Fatalf("GetPublicNote: %+v err=%v", pn, err)
	}
	if pn.Title != "附件笔记" || pn.ContentMD != content {
		t.Fatalf("public note fields: %+v", pn)
	}

	// 附件归属：先建 files.objects ready 行 + note_attachments 行
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO files.objects (id, user_id, sha256, size_bytes, bucket, object_key, status)
		VALUES ($1, $2, $3, 10, 'biumind-files-test', $4, 'ready')
	`, fileID, uid, strings.Repeat("a", 64), uid.String()+"/"+fileID.String()); err != nil {
		t.Fatalf("insert files.objects: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO brain.note_attachments (note_id, file_id, is_associated)
		VALUES ($1, $2, true)
	`, n.ID, fileID); err != nil {
		t.Fatalf("insert note_attachments: %v", err)
	}
	ok, err := h.st.AttachmentBelongs(ctx, n.ID, fileID)
	if err != nil || !ok {
		t.Fatalf("AttachmentBelongs own: %v %v", ok, err)
	}
	ok, err = h.st.AttachmentBelongs(ctx, n.ID, uuid.New())
	if err != nil || ok {
		t.Fatalf("AttachmentBelongs stranger should be false: %v %v", ok, err)
	}

	// object_key 查询：ready 命中；pending 不命中
	key, err := h.st.GetSharedFileObjectKey(ctx, fileID)
	if err != nil || key == "" {
		t.Fatalf("GetSharedFileObjectKey: %q %v", key, err)
	}
	pendingID := uuid.New()
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO files.objects (id, user_id, sha256, size_bytes, bucket, object_key, status)
		VALUES ($1, $2, $3, 10, 'biumind-files-test', $4, 'pending')
	`, pendingID, uid, strings.Repeat("b", 64), uid.String()+"/"+pendingID.String()); err != nil {
		t.Fatalf("insert pending files.objects: %v", err)
	}
	if _, err := h.st.GetSharedFileObjectKey(ctx, pendingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending object should be ErrNotFound, got %v", err)
	}

	// biu-file:// 改写：URI 精确形态被替换，裸字样不动
	rw := RewriteShareFileURIs(content, "tok123")
	want := fmt.Sprintf("/v1/shares/tok123/files/%s", fileID)
	if !strings.Contains(rw, want) {
		t.Fatalf("rewrite missing %q: %s", want, rw)
	}
	if strings.Contains(rw, "biu-file://"+fileID.String()) {
		t.Fatalf("uri not rewritten: %s", rw)
	}
	if !strings.Contains(rw, "biu-file:// 字样") {
		t.Fatalf("plain text should stay: %s", rw)
	}

	// 软删进回收站 → DeletedAt 非空（公开链 ④ 判 410 的依据）
	if err := h.st.SoftDeleteNote(ctx, n.ID, uid, uid.String()); err != nil {
		t.Fatalf("SoftDeleteNote: %v", err)
	}
	pn, err = h.st.GetPublicNote(ctx, n.ID)
	if err != nil || pn.DeletedAt == nil {
		t.Fatalf("trashed note should carry deleted_at: %+v err=%v", pn, err)
	}
}

// ─── S2：max_views 三态 / 会话去重 / TTL 清理 ────────────

func TestShareMaxViewsTriState(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	ctx := context.Background()
	n := createNote(t, h, uid, "上限三态", "正文")

	// 新建缺省 → NULL（不限）
	sh := upsertShare(t, h, n.ID, uid, UpsertShareInput{})
	if sh.MaxViews != nil {
		t.Fatalf("create default max_views should be NULL: %+v", sh)
	}
	// 设置 100 → 缺省保持 → 0 移除（NULL）
	mv := 100
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{MaxViewsSet: true, MaxViews: &mv})
	if sh.MaxViews == nil || *sh.MaxViews != 100 {
		t.Fatalf("max_views set: %+v", sh)
	}
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{})
	if sh.MaxViews == nil || *sh.MaxViews != 100 {
		t.Fatalf("omitted max_views should keep: %+v", sh)
	}
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{MaxViewsSet: true, MaxViews: nil})
	if sh.MaxViews != nil {
		t.Fatalf("max_views=0 should remove limit: %+v", sh)
	}
	// 停用恢复缺省 → 保持（先设回 50 再停用）
	mv = 50
	upsertShare(t, h, n.ID, uid, UpsertShareInput{MaxViewsSet: true, MaxViews: &mv})
	if err := h.st.DisableShare(ctx, n.ID, uid, uid.String()); err != nil {
		t.Fatalf("DisableShare: %v", err)
	}
	sh = upsertShare(t, h, n.ID, uid, UpsertShareInput{Token: "must-not-be-used"})
	if sh.MaxViews == nil || *sh.MaxViews != 50 || sh.DisabledAt != nil {
		t.Fatalf("restore should keep max_views: %+v", sh)
	}
}

func TestShareViewSessionDedup(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	ctx := context.Background()
	n := createNote(t, h, uid, "去重", "正文")
	sh := upsertShare(t, h, n.ID, uid, UpsertShareInput{})

	// 同 session 两次 → 只计 1 次
	s1 := "hash-session-1"
	counted, err := h.st.RecordShareView(ctx, sh.ID, &s1)
	if err != nil || !counted {
		t.Fatalf("first view should count: %v %v", counted, err)
	}
	counted, err = h.st.RecordShareView(ctx, sh.ID, &s1)
	if err != nil || counted {
		t.Fatalf("same session repeat should not count: %v %v", counted, err)
	}
	// 不同 session → 各计 1 次
	s2 := "hash-session-2"
	counted, err = h.st.RecordShareView(ctx, sh.ID, &s2)
	if err != nil || !counted {
		t.Fatalf("new session should count: %v %v", counted, err)
	}
	// 无 session → 每次照计
	counted, err = h.st.RecordShareView(ctx, sh.ID, nil)
	if err != nil || !counted {
		t.Fatalf("no session should always count: %v %v", counted, err)
	}
	got, err := h.st.GetShareByToken(ctx, sh.Token)
	if err != nil {
		t.Fatalf("GetShareByToken: %v", err)
	}
	if got.ViewCount != 3 {
		t.Fatalf("view_count: got %d want 3", got.ViewCount)
	}
	// 去重记录确实落了两行
	var rows int
	if err := h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM brain.note_share_view_sessions WHERE share_id = $1
	`, sh.ID).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("session rows: %d err=%v", rows, err)
	}
}

func TestPruneShareViewSessions(t *testing.T) {
	h := newStoreHarness(t)
	uid := uuid.New()
	defer h.cleanupShares(t, uid)
	defer h.cleanupNotes(t, uid)
	ctx := context.Background()
	n := createNote(t, h, uid, "清理", "正文")
	sh := upsertShare(t, h, n.ID, uid, UpsertShareInput{})

	// 两条会话记录，一条回溯到 40 天前
	sOld, sNew := "hash-old", "hash-new"
	for _, s := range []string{sOld, sNew} {
		if _, err := h.st.RecordShareView(ctx, sh.ID, &s); err != nil {
			t.Fatalf("RecordShareView: %v", err)
		}
	}
	if _, err := h.pool.Exec(ctx, `
		UPDATE brain.note_share_view_sessions
		SET created_at = now() - interval '40 days'
		WHERE share_id = $1 AND session_hash = $2
	`, sh.ID, sOld); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	deleted, err := h.st.PruneShareViewSessions(ctx, 30)
	if err != nil {
		t.Fatalf("PruneShareViewSessions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 pruned, got %d", deleted)
	}
	var remain int
	if err := h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM brain.note_share_view_sessions WHERE share_id = $1
	`, sh.ID).Scan(&remain); err != nil || remain != 1 {
		t.Fatalf("remain: %d err=%v", remain, err)
	}
	// 默认 keepDays（0 → 30 天兜底）同样只删过期行
	if _, err := h.st.PruneShareViewSessions(ctx, 0); err != nil {
		t.Fatalf("PruneShareViewSessions default: %v", err)
	}
}
