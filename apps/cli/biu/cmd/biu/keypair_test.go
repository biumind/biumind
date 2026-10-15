package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/agentcrypto"
)

// 生成 → 存 0600 文件(hex) → 二次读出一致。
func TestLoadOrCreateKeypair_GenerateThenLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	priv1, pub1, err := loadOrCreateKeypair()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if len(priv1) != agentcrypto.X25519KeySize {
		t.Fatalf("priv len=%d", len(priv1))
	}
	// 文件里应是 hex，0600。
	path := filepath.Join(home, ".biu", "agentplane", "privkey")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read privkey file: %v", err)
	}
	if string(bytes.TrimSpace(raw)) != hex.EncodeToString(priv1) {
		t.Fatal("privkey file should hold hex")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm=%o want 600", fi.Mode().Perm())
	}
	// 二次读出一致（不重新生成）。
	priv2, pub2, err := loadOrCreateKeypair()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(priv1, priv2) || !bytes.Equal(pub1, pub2) {
		t.Fatal("second load should return the same keypair")
	}
}

// R6.2 旧 raw 32B 文件 → 迁移为 hex 覆写同文件。
func TestLoadOrCreateKeypair_MigratesLegacyRawFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	rawPriv, rawPub, err := agentcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".biu", "agentplane", "privkey")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, rawPriv, 0o600); err != nil {
		t.Fatal(err)
	}

	priv, pub, err := loadOrCreateKeypair()
	if err != nil {
		t.Fatalf("migrate load: %v", err)
	}
	if !bytes.Equal(priv, rawPriv) || !bytes.Equal(pub, rawPub) {
		t.Fatal("migrated keypair should equal legacy")
	}
	// 同文件应已被 hex 覆写。
	body, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	if string(bytes.TrimSpace(body)) != hex.EncodeToString(rawPriv) {
		t.Errorf("legacy raw file should be overwritten with hex")
	}
}

// device token：0600 文件存取。
func TestDeviceToken_FileRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if loadDeviceToken() != "" {
		t.Fatal("expected empty before save")
	}
	if err := saveDeviceToken("biu_dev_abc"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadDeviceToken(); got != "biu_dev_abc" {
		t.Errorf("load=%q want biu_dev_abc", got)
	}
	fi, err := os.Stat(filepath.Join(home, ".biu", "device_token"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm=%o want 600", fi.Mode().Perm())
	}
}
