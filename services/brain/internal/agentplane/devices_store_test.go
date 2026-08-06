package agentplane

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 纯函数：配对码 8 位数字 + sha256Hex 确定性。
func TestRandomDigitsAndHash(t *testing.T) {
	code, err := randomDigits(8)
	if err != nil || len(code) != 8 {
		t.Fatalf("randomDigits(8) = %q,%v", code, err)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Fatalf("code has non-digit: %q", code)
		}
	}
	if string(sha256Hex("x")) != string(sha256Hex("x")) {
		t.Fatal("sha256Hex not deterministic")
	}
	if string(sha256Hex("a")) == string(sha256Hex("b")) {
		t.Fatal("sha256Hex collision on a/b")
	}
}

// VerifyDeviceToken 必须拒绝**已过期**的 token（expires_at > now() 强制）。
// 吊销 / 坏 token 已由 TestDevicePairingFlow 覆盖；这里专补过期分支。
func TestVerifyDeviceToken_Expired(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	uid := uuid.New()

	// 直接插一条 token_hash 已知、但 expires_at 在过去的 device 行。
	expiredTok := "biu_dev_expired_secret"
	expiredID := uuid.New()
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, 'stale-mac', $3, 'pfx', now() - interval '1 hour')`,
		expiredID, uid, sha256Hex(expiredTok)); err != nil {
		t.Fatalf("seed expired device: %v", err)
	}
	if _, _, err := h.store.VerifyDeviceToken(ctx, expiredTok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token should be rejected, got %v", err)
	}

	// 对照：同形态但未过期 → 通过。
	liveTok := "biu_dev_live_secret"
	liveID := uuid.New()
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, 'live-mac', $3, 'pfx', now() + interval '1 hour')`,
		liveID, uid, sha256Hex(liveTok)); err != nil {
		t.Fatalf("seed live device: %v", err)
	}
	gotUID, gotDev, err := h.store.VerifyDeviceToken(ctx, liveTok)
	if err != nil || gotUID != uid || gotDev != liveID {
		t.Fatalf("live token verify: uid=%v dev=%v err=%v", gotUID, gotDev, err)
	}
}

// DB 流程：request → approve → poll(mint) → VerifyDeviceToken → revoke → verify fails。
// 需 DATABASE_URL（CI）；本地无则 skip。
func TestDevicePairingFlow(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping device pairing DB test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	_, _ = pool.Exec(ctx, `TRUNCATE agent_pairings, agent_devices`)
	store := NewStore(pool)
	uid := uuid.New()

	// 1. request
	p, err := store.CreatePairing(ctx, "didi-mbp", "darwin/arm64", "biu_daemon")
	if err != nil {
		t.Fatalf("CreatePairing: %v", err)
	}
	if len(p.Code) != 8 || p.PairingSecret == "" {
		t.Fatalf("bad pairing: %+v", p)
	}

	// 2. poll before approve → pending
	if _, _, status, err := store.PollPairing(ctx, p.PairingID, p.PairingSecret); err != nil || status != "pending" {
		t.Fatalf("poll pending: status=%q err=%v", status, err)
	}
	// wrong secret → ErrPairingSecret
	if _, _, _, err := store.PollPairing(ctx, p.PairingID, "wrong"); !errors.Is(err, ErrPairingSecret) {
		t.Fatalf("poll wrong secret should ErrPairingSecret, got %v", err)
	}

	// 3. approve (wrong code → ErrNotFound)
	if _, err := store.ApprovePairing(ctx, uid, "00000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approve wrong code should ErrNotFound, got %v", err)
	}
	if mn, err := store.ApprovePairing(ctx, uid, p.Code); err != nil || mn != "didi-mbp" {
		t.Fatalf("approve: mn=%q err=%v", mn, err)
	}

	// 4. poll after approve → mint token
	token, dev, status, err := store.PollPairing(ctx, p.PairingID, p.PairingSecret)
	if err != nil || status != "approved" || !strings.HasPrefix(token, deviceTokenPrefix) || dev == nil {
		t.Fatalf("poll approved: token=%q status=%q dev=%v err=%v", token, status, dev, err)
	}
	// second poll → consumed
	if _, _, status, _ := store.PollPairing(ctx, p.PairingID, p.PairingSecret); status != "consumed" {
		t.Fatalf("second poll should be consumed, got %q", status)
	}

	// 5. verify token → user
	gotUID, gotDev, err := store.VerifyDeviceToken(ctx, token)
	if err != nil || gotUID != uid || gotDev != dev.DeviceID {
		t.Fatalf("verify: uid=%v dev=%v err=%v", gotUID, gotDev, err)
	}
	// bad token → ErrNotFound
	if _, _, err := store.VerifyDeviceToken(ctx, "biu_dev_deadbeef_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verify bad token should ErrNotFound, got %v", err)
	}

	// 6. revoke → verify fails
	if err := store.RevokeDevice(ctx, uid, dev.DeviceID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := store.VerifyDeviceToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verify after revoke should fail, got %v", err)
	}
	// revoke again → ErrNotFound (already revoked)
	if err := store.RevokeDevice(ctx, uid, dev.DeviceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke should ErrNotFound, got %v", err)
	}
}
