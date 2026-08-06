package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/oskeychain"
	"github.com/biumind/biumind/packages/go-sdk/biu/agentcrypto"
)

type fakeKC struct{ m map[string]string }

func (f *fakeKC) Name() string { return "fake" }
func (f *fakeKC) Get(s, a string) (string, bool, error) {
	v, ok := f.m[s+"/"+a]
	return v, ok, nil
}
func (f *fakeKC) Set(s, a, v string) error { f.m[s+"/"+a] = v; return nil }
func (f *fakeKC) Delete(s, a string) error { delete(f.m, s+"/"+a); return nil }

// 生成 → 存 keychain(hex) → 二次读出一致。
func TestLoadOrCreateKeypair_GenerateThenLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeKC{m: map[string]string{}}
	defer oskeychain.SetForTest(fake)()

	priv1, pub1, err := loadOrCreateKeypair()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if len(priv1) != agentcrypto.X25519KeySize {
		t.Fatalf("priv len=%d", len(priv1))
	}
	// keychain 里应是 hex。
	if v, ok, _ := fake.Get(keychainServiceName, agentplanePrivkeyAccnt); !ok || v != hex.EncodeToString(priv1) {
		t.Fatalf("keychain privkey mismatch")
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

// R6.2 旧 raw 32B 文件 → 迁移到 keychain(hex) + 删旧文件。
func TestLoadOrCreateKeypair_MigratesLegacyRawFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := &fakeKC{m: map[string]string{}}
	defer oskeychain.SetForTest(fake)()

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
	// 旧 raw 文件应被删，keychain 里应是 hex。
	if _, statErr := os.Stat(legacy); !os.IsNotExist(statErr) {
		t.Errorf("legacy raw file should be removed after keychain migrate")
	}
	if v, ok, _ := fake.Get(keychainServiceName, agentplanePrivkeyAccnt); !ok || v != hex.EncodeToString(rawPriv) {
		t.Errorf("keychain should hold hex privkey after migrate")
	}
}

// device token：keychain 存取 + 老明文文件迁移。
func TestDeviceToken_KeychainAndMigrate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := &fakeKC{m: map[string]string{}}
	defer oskeychain.SetForTest(fake)()

	if loadDeviceToken() != "" {
		t.Fatal("expected empty before save")
	}
	if err := saveDeviceToken("biu_dev_abc"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadDeviceToken(); got != "biu_dev_abc" {
		t.Errorf("load=%q want biu_dev_abc", got)
	}
	if v, ok, _ := fake.Get(keychainServiceName, deviceTokenAccount); !ok || v != "biu_dev_abc" {
		t.Errorf("keychain device-token mismatch")
	}
}
