package store

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 复用跟 credits_test.go 同款的测试 PG 连接策略。
//
// 默认 dev compose:
//
//	postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core
//
// 设 IDENTITY_TEST_DATABASE_URL=skip 跳过整组。
func storeTestDBURL() string {
	if v := os.Getenv("IDENTITY_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://biumind:biumind_dev_password_change_me@localhost:5432/biu_core?sslmode=disable"
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	url := storeTestDBURL()
	if url == "skip" {
		t.Skip("IDENTITY_TEST_DATABASE_URL=skip")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("PG unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

// 创建一个临时 user, 测试结束清掉。
func newTestUser(t *testing.T, s *Store) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO identity.users (id, email) VALUES ($1, $2)`,
		uid, "rot-test-"+uid.String()[:8]+"@example.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(),
			`DELETE FROM identity.refresh_tokens WHERE user_id = $1`, uid)
		_, _ = s.pool.Exec(context.Background(),
			`DELETE FROM identity.users WHERE id = $1`, uid)
	})
	return uid
}

func randHash(t *testing.T) []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRotateRefreshToken_Success — 标准 rotation:老行 revoke,新行继承
// installation_id + absolute_expires_at,新 expires_at 续了 sliding 窗口。
func TestRotateRefreshToken_Success(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	// 先建一个初始 row
	oldHash := randHash(t)
	oldID, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-A", oldHash, "Mac",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatalf("create initial: %v", err)
	}

	old, err := s.FindRefreshToken(ctx, oldHash)
	if err != nil {
		t.Fatalf("find old: %v", err)
	}
	oldAbs := old.AbsoluteExpiresAt

	// rotate
	newHash := randHash(t)
	newID, newExpiresAt, newAbs, err := s.RotateRefreshToken(
		ctx, oldID, newHash, nil, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0",
	)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newID == oldID {
		t.Error("new ID should differ from old")
	}
	if !newAbs.Equal(oldAbs) {
		t.Errorf("absolute_expires_at not preserved: old=%v new=%v", oldAbs, newAbs)
	}
	if time.Until(newExpiresAt) < 89*24*time.Hour {
		t.Errorf("new sliding expires_at too soon: %v", newExpiresAt)
	}

	// 老行: 仍能 Find,但 RevokedAt != nil
	revoked, err := s.FindRefreshToken(ctx, oldHash)
	if err != nil {
		t.Fatalf("find revoked: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Error("old token should be revoked")
	}

	// 新行: active, hash 找得到
	cur, err := s.FindRefreshToken(ctx, newHash)
	if err != nil {
		t.Fatalf("find new: %v", err)
	}
	if cur.RevokedAt != nil {
		t.Error("new token should be active")
	}
	if cur.InstallationID != "install-A" {
		t.Errorf("installation_id not inherited: %q", cur.InstallationID)
	}
	if cur.DeviceName != "Mac" {
		t.Errorf("device_name not inherited: %q", cur.DeviceName)
	}
	// rotation 同事务记录 ip / ua / last_used_at (迟恢复判定依据)
	if cur.LastIP == nil || *cur.LastIP != "10.0.0.1" {
		t.Errorf("rotation should record last_ip=10.0.0.1, got %v", cur.LastIP)
	}
	if cur.LastUA == nil || *cur.LastUA != "rotation-test/1.0" {
		t.Errorf("rotation should record last_ua, got %v", cur.LastUA)
	}
	if cur.LastUsedAt == nil {
		t.Error("rotation should set last_used_at")
	}
}

// TestRotateRefreshToken_RotatedTwice_OldRotateFails — 拿一个老 ID 调
// rotate, 拿到新 row;再用同一老 ID 调 rotate, 应该返 ErrNotFound (UPDATE
// WHERE revoked_at IS NULL 命中 0 行)。这是 reuse detection 的 store 层
// 触发条件 — handleRefresh 据此返 token_reuse 401。
func TestRotateRefreshToken_RotatedTwice_OldRotateFails(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	hash1 := randHash(t)
	oldID, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-B", hash1, "PC",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}

	// 1st rotate ok
	if _, _, _, err := s.RotateRefreshToken(ctx, oldID, randHash(t), nil, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0"); err != nil {
		t.Fatalf("first rotate: %v", err)
	}

	// 2nd rotate same oldID → 必须 ErrNotFound
	_, _, _, err = s.RotateRefreshToken(ctx, oldID, randHash(t), nil, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound on rotating an already-revoked row, got %v", err)
	}
}

// TestRotateRefreshToken_AbsoluteCapPreserved — 多次 rotation 都不重置
// absolute_expires_at, 它一直等于最初 created_at + absoluteTTL。这是绝对
// cap 的核心不变量。
func TestRotateRefreshToken_AbsoluteCapPreserved(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	hash := randHash(t)
	id, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-C", hash, "iPhone",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := s.FindRefreshToken(ctx, hash)
	originalAbs := row.AbsoluteExpiresAt

	// rotate 5 次
	currID := id
	for i := 0; i < 5; i++ {
		newHash := randHash(t)
		newID, _, gotAbs, err := s.RotateRefreshToken(ctx, currID, newHash, nil, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0")
		if err != nil {
			t.Fatalf("rotate iter %d: %v", i, err)
		}
		if !gotAbs.Equal(originalAbs) {
			t.Errorf("iter %d: absolute_expires_at drifted: orig=%v got=%v",
				i, originalAbs, gotAbs)
		}
		currID = newID
	}
}

// TestRevokeFamilyByInstallation — 撤销 (user, install) 下所有 active,
// 不动其他 installation, 不重复撤已 revoked 的。
func TestRevokeFamilyByInstallation(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	// install-X: 一个 active
	hashX1 := randHash(t)
	if _, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-X", hashX1, "X1",
		90*24*time.Hour, 365*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	// install-Y: 一个 active (不同 installation)
	hashY := randHash(t)
	if _, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-Y", hashY, "Y",
		90*24*time.Hour, 365*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}
	// install-X 又一个 已 revoked 的(模拟 rotate 过的老行)
	hashX2 := randHash(t)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.refresh_tokens
		    (user_id, installation_id, token_hash, device_name,
		     expires_at, absolute_expires_at, revoked_at)
		VALUES ($1, 'install-X', $2, 'X-old',
		        now() + interval '90 days',
		        now() + interval '365 days',
		        now() - interval '1 hour')
	`, uid, hashX2)
	if err != nil {
		t.Fatal(err)
	}

	revoked, err := s.RevokeFamilyByInstallation(ctx, uid, "install-X")
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Errorf("expected 1 active row revoked, got %d", revoked)
	}

	// install-X 那条 active 现在 revoked
	x1, _ := s.FindRefreshToken(ctx, hashX1)
	if x1.RevokedAt == nil {
		t.Error("install-X active should be revoked")
	}
	// install-Y 不动
	y, _ := s.FindRefreshToken(ctx, hashY)
	if y.RevokedAt != nil {
		t.Error("install-Y active should NOT be revoked")
	}
}

// TestRevokeFamilyByInstallation_EmptyInstallationIDIsNoop — 老客户端
// installation_id=” 不参与家族识别 (防止误伤所有"老客户端"用户)。
func TestRevokeFamilyByInstallation_EmptyInstallationIDIsNoop(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	hash := randHash(t)
	if _, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "", hash, "old",
		90*24*time.Hour, 365*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}

	revoked, err := s.RevokeFamilyByInstallation(ctx, uid, "")
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 0 {
		t.Errorf("empty installation_id should be no-op, got %d revoked", revoked)
	}
	// 行还活着
	got, _ := s.FindRefreshToken(ctx, hash)
	if got.RevokedAt != nil {
		t.Error("legacy empty-installation row should not be revoked")
	}
}

// TestInsertSecurityEvent — 写一行 + 字段被持久化。
func TestInsertSecurityEvent(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	err := s.InsertSecurityEvent(ctx, SecurityEvent{
		UserID: uid,
		Kind:   "refresh_token_reuse",
		Detail: []byte(`{"session_id":"abc","family_revoked":2}`),
		IP:     "192.168.1.1",
		UA:     "Mozilla/5.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		kind     string
		detail   []byte
		ip       *string
		ua       *string
		hostFunc string
	)
	err = s.pool.QueryRow(ctx, `
		SELECT kind, detail::text, host(ip), ua FROM identity.security_events
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1
	`, uid).Scan(&kind, &detail, &ip, &ua)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "refresh_token_reuse" {
		t.Errorf("kind=%q", kind)
	}
	if ip == nil || *ip != "192.168.1.1" {
		t.Errorf("ip=%v", ip)
	}
	if ua == nil || *ua != "Mozilla/5.0" {
		t.Errorf("ua=%v", ua)
	}
	_ = hostFunc
	_ = detail
	// 清理
	_, _ = s.pool.Exec(ctx, `DELETE FROM identity.security_events WHERE user_id = $1`, uid)
}

// TestReuseDetectionFlow — 模拟 handleRefresh 在 reuse detection 命中时
// 走的完整路径,验证 store 层提供的所有原语合起来 work:
//
//  1. 攻击者偷了 hashAttacker, 抢先 rotate → server 给攻击者 hashAttackerNew
//  2. 用户合法客户端持有的还是 hashAttacker, 提交 → FindRefreshToken 看到
//     该行 revoked_at != nil → 触发 reuse detection
//  3. handler 调 RevokeFamilyByInstallation → 撤销该 (user, install) 整族
//  4. handler 写 security_events
//  5. 攻击者下次再用 hashAttackerNew → FindRefreshToken 也看到 revoked_at !=
//     nil → 同样触发 reuse detection (再撤一次族, 但已经是 0 行 active)
//
// 完整把流程串起来,确保 "store 提供的原语足够实现 handler 设计"。
func TestReuseDetectionFlow(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	// 1) 用户登录, 拿到 (hash0, oldID)
	hash0 := randHash(t)
	oldID, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-victim", hash0, "Victim",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}

	// 2) 攻击者偷了 hash0, 抢先 rotate → 拿到 hashAttackerNew
	hashAttackerNew := randHash(t)
	if _, _, _, err := s.RotateRefreshToken(ctx, oldID, hashAttackerNew, nil, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0"); err != nil {
		t.Fatalf("attacker first rotate: %v", err)
	}

	// 3) 用户合法客户端拿 hash0 来 refresh — 模拟 handleRefresh 的检查
	t0, err := s.FindRefreshToken(ctx, hash0)
	if err != nil {
		t.Fatalf("find old: %v", err)
	}
	if t0.RevokedAt == nil {
		t.Fatal("hash0 should be revoked after attacker's rotate")
	}
	// → handler 触发 reuse detection
	revoked, err := s.RevokeFamilyByInstallation(ctx, t0.UserID, t0.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Errorf("expected attacker's new row to be revoked (1), got %d", revoked)
	}
	if err := s.InsertSecurityEvent(ctx, SecurityEvent{
		UserID: t0.UserID,
		Kind:   "refresh_token_reuse",
		Detail: []byte(`{"reason":"victim resubmitted old token"}`),
		IP:     "1.2.3.4",
		UA:     "VictimClient/1.0",
	}); err != nil {
		t.Fatal(err)
	}

	// 4) 攻击者下次用 hashAttackerNew — 看到 revoked_at != nil
	t1, err := s.FindRefreshToken(ctx, hashAttackerNew)
	if err != nil {
		t.Fatalf("find attacker token: %v", err)
	}
	if t1.RevokedAt == nil {
		t.Fatal("attacker token should be revoked after family revoke")
	}

	// 5) 验证 family 现在没 active 行了
	revokedAgain, _ := s.RevokeFamilyByInstallation(ctx, uid, "install-victim")
	if revokedAgain != 0 {
		t.Errorf("second family revoke should be no-op, got %d", revokedAgain)
	}

	// 6) 验证 security_event 落表
	var count int
	_ = s.pool.QueryRow(ctx,
		`SELECT count(*) FROM identity.security_events WHERE user_id = $1 AND kind = 'refresh_token_reuse'`,
		uid).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 security_event, got %d", count)
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM identity.security_events WHERE user_id = $1`, uid)
}

// TestReapRefreshTokens — revoked 久了的 + absolute 过期的物理删除,active 不动。
func TestReapRefreshTokens(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	// 1) 一个 active 的 — 不应被删
	activeHash := randHash(t)
	if _, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-active", activeHash, "active",
		90*24*time.Hour, 365*24*time.Hour,
	); err != nil {
		t.Fatal(err)
	}

	// 2) 一个 revoked_at = 31 天前的 — 应被删
	staleRevokedHash := randHash(t)
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO identity.refresh_tokens
		    (user_id, installation_id, token_hash, device_name,
		     expires_at, absolute_expires_at, revoked_at, created_at)
		VALUES ($1, $2, $3, 'stale-revoked',
		        now() + interval '90 days',
		        now() + interval '365 days',
		        now() - interval '31 days',
		        now() - interval '32 days')
	`, uid, "install-stale-revoked", staleRevokedHash)

	// 3) 一个 absolute_expires_at = 8 天前的 (sliding 没过 — 不重要) — 应被删
	staleAbsHash := randHash(t)
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO identity.refresh_tokens
		    (user_id, installation_id, token_hash, device_name,
		     expires_at, absolute_expires_at, created_at)
		VALUES ($1, $2, $3, 'stale-abs',
		        now() + interval '1 day',
		        now() - interval '8 days',
		        now() - interval '400 days')
	`, uid, "install-stale-abs", staleAbsHash)

	// 4) 刚 revoked 的 (1 小时前) — 不应被删 (在保留窗口内)
	freshRevokedHash := randHash(t)
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO identity.refresh_tokens
		    (user_id, installation_id, token_hash, device_name,
		     expires_at, absolute_expires_at, revoked_at, created_at)
		VALUES ($1, $2, $3, 'fresh-revoked',
		        now() + interval '90 days',
		        now() + interval '365 days',
		        now() - interval '1 hour',
		        now() - interval '2 hour')
	`, uid, "install-fresh-revoked", freshRevokedHash)

	deleted, err := s.ReapRefreshTokens(ctx, 30*24*time.Hour, 7*24*time.Hour, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 reaped (stale-revoked + stale-abs), got %d", deleted)
	}

	// active 仍在
	if _, err := s.FindRefreshToken(ctx, activeHash); err != nil {
		t.Errorf("active row was reaped, should not be: %v", err)
	}
	// fresh revoked 仍在
	if _, err := s.FindRefreshToken(ctx, freshRevokedHash); err != nil {
		t.Errorf("fresh-revoked row was reaped, should not be (within retention): %v", err)
	}
	// stale 都没了
	if _, err := s.FindRefreshToken(ctx, staleRevokedHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale-revoked should be gone, got %v", err)
	}
	if _, err := s.FindRefreshToken(ctx, staleAbsHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale-abs should be gone, got %v", err)
	}
}

// TestGraceReplayHead_ChainWalk — A→B→C 链: 从 A 出发应走到 head C,
// 返回的密文是 B.rotated_token_enc (= C 的 token 密文), head expires_at 是
// C 行的 sliding 窗口。
func TestGraceReplayHead_ChainWalk(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	hashA := randHash(t)
	idA, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-grace", hashA, "Mac",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}

	encB := []byte("enc-of-B")
	idB, _, _, err := s.RotateRefreshToken(ctx, idA, randHash(t), encB, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0")
	if err != nil {
		t.Fatalf("rotate A→B: %v", err)
	}
	encC := []byte("enc-of-C")
	idC, expC, _, err := s.RotateRefreshToken(ctx, idB, randHash(t), encC, 90*24*time.Hour, "10.0.0.2", "rotation-test/2.0")
	if err != nil {
		t.Fatalf("rotate B→C: %v", err)
	}

	// 从 A 出发 (两跳) → head C + C 的密文; head IP/UA 取自 C 行 (B→C 的轮换端)
	head, ok, err := s.GraceReplayHead(ctx, idA)
	if err != nil || !ok {
		t.Fatalf("replay from A: ok=%v err=%v", ok, err)
	}
	if head.ID != idC {
		t.Errorf("head = %v, want C %v", head.ID, idC)
	}
	if string(head.TokenEnc) != string(encC) {
		t.Errorf("head enc = %q, want enc-of-C", head.TokenEnc)
	}
	if !head.ExpiresAt.Equal(expC) {
		t.Errorf("head expires_at = %v, want C row %v", head.ExpiresAt, expC)
	}
	if head.Hops != 2 {
		t.Errorf("A→C hops = %d, want 2", head.Hops)
	}
	if head.IP == nil || *head.IP != "10.0.0.2" {
		t.Errorf("head IP = %v, want 10.0.0.2 (B→C 轮换端)", head.IP)
	}
	if head.UA == nil || *head.UA != "rotation-test/2.0" {
		t.Errorf("head UA = %v, want rotation-test/2.0", head.UA)
	}

	// 从 B 出发 (一跳) → 同样到 C
	head, ok, err = s.GraceReplayHead(ctx, idB)
	if err != nil || !ok {
		t.Fatalf("replay from B: ok=%v err=%v", ok, err)
	}
	if head.ID != idC || string(head.TokenEnc) != string(encC) {
		t.Errorf("replay from B: head=%v enc=%q", head.ID, head.TokenEnc)
	}
	if head.Hops != 1 {
		t.Errorf("B→C hops = %d, want 1", head.Hops)
	}

	// 老行写了链指针: A.rotated_to = B
	var rotatedTo *uuid.UUID
	var storedEnc []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT rotated_to, rotated_token_enc
		  FROM identity.refresh_tokens WHERE id = $1
	`, idA).Scan(&rotatedTo, &storedEnc); err != nil {
		t.Fatal(err)
	}
	if rotatedTo == nil || *rotatedTo != idB {
		t.Errorf("A.rotated_to = %v, want B %v", rotatedTo, idB)
	}
	if string(storedEnc) != string(encB) {
		t.Errorf("A.rotated_token_enc = %q, want enc-of-B", storedEnc)
	}
}

// TestGraceReplayHead_BrokenChain — logout 等非 rotate 撤销 (rotated_to NULL)
// / 缺密文 / 行不存在 / 未 rotate 的 active 行 → ok=false。
func TestGraceReplayHead_BrokenChain(t *testing.T) {
	s := newTestStore(t)
	uid := newTestUser(t, s)
	ctx := context.Background()

	// 1) logout 撤销 (revoked_at 置位但 rotated_to NULL) → 断链
	hashL := randHash(t)
	idL, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-logout", hashL, "PC",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeRefreshToken(ctx, hashL); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GraceReplayHead(ctx, idL); err != nil || ok {
		t.Errorf("logout-revoked row: ok=%v err=%v, want ok=false", ok, err)
	}

	// 2) rotate 时没传密文 (successorEnc=nil) → 链指针在但 replay 不命中
	hashN := randHash(t)
	idN, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-noenc", hashN, "PC",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.RotateRefreshToken(ctx, idN, randHash(t), nil, 90*24*time.Hour, "10.0.0.1", "rotation-test/1.0"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GraceReplayHead(ctx, idN); err != nil || ok {
		t.Errorf("no-enc rotated row: ok=%v err=%v, want ok=false", ok, err)
	}

	// 3) 不存在的行
	if _, ok, err := s.GraceReplayHead(ctx, uuid.New()); err != nil || ok {
		t.Errorf("missing row: ok=%v err=%v, want ok=false", ok, err)
	}

	// 4) 还 active 的行 (没发生过 rotate) → 防御性 false
	hashA := randHash(t)
	idA, err := s.CreateOrRotateRefreshToken(
		ctx, uid, "install-active", hashA, "Mac",
		90*24*time.Hour, 365*24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GraceReplayHead(ctx, idA); err != nil || ok {
		t.Errorf("active row: ok=%v err=%v, want ok=false", ok, err)
	}
}
