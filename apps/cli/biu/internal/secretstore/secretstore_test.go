package secretstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/oskeychain"
)

// fakeKeychain — in-memory oskeychain.Keychain for tests.
type fakeKeychain struct {
	m map[string]string
}

func newFake() *fakeKeychain { return &fakeKeychain{m: map[string]string{}} }

func key(s, a string) string { return s + "\x00" + a }

func (f *fakeKeychain) Name() string { return "fake" }
func (f *fakeKeychain) Get(s, a string) (string, bool, error) {
	v, ok := f.m[key(s, a)]
	return v, ok, nil
}
func (f *fakeKeychain) Set(s, a, v string) error { f.m[key(s, a)] = v; return nil }
func (f *fakeKeychain) Delete(s, a string) error { delete(f.m, key(s, a)); return nil }

func TestSecretStore_KeychainRoundTrip(t *testing.T) {
	restore := oskeychain.SetForTest(newFake())
	defer restore()

	file := filepath.Join(t.TempDir(), "secret")
	s := Open("svc", "acct", file)
	if s.Backend() != "fake" {
		t.Fatalf("backend=%q want fake", s.Backend())
	}
	if v, _ := s.Get(); v != "" {
		t.Errorf("empty Get=%q want \"\"", v)
	}
	if err := s.Set("hello"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, _ := s.Get(); v != "hello" {
		t.Errorf("Get=%q want hello", v)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if v, _ := s.Get(); v != "" {
		t.Errorf("after delete Get=%q want \"\"", v)
	}
}

func TestSecretStore_FileFallback(t *testing.T) {
	restore := oskeychain.SetForTest(nil) // 模拟无 keychain
	defer restore()

	file := filepath.Join(t.TempDir(), "sub", "secret")
	s := Open("svc", "acct", file)
	if s.Backend() != "file" {
		t.Fatalf("backend=%q want file", s.Backend())
	}
	if err := s.Set("tok123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// 0600 + 内容
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm=%o want 600", fi.Mode().Perm())
	}
	if v, _ := s.Get(); v != "tok123" {
		t.Errorf("Get=%q want tok123", v)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file should be gone after delete")
	}
}

// keychain 空但有 legacy 文件 → Get 回退读文件；Set 后迁移到 keychain + 删文件。
func TestSecretStore_MigratesLegacyFile(t *testing.T) {
	fake := newFake()
	restore := oskeychain.SetForTest(fake)
	defer restore()

	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte("legacy-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open("svc", "acct", file)

	// keychain 无 → 回退读 legacy 文件。
	if v, _ := s.Get(); v != "legacy-token" {
		t.Errorf("legacy Get=%q want legacy-token", v)
	}
	// Set 写 keychain + 删 legacy 文件。
	if err := s.Set("new-token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("legacy file should be removed after keychain migrate")
	}
	if v, ok, _ := fake.Get("svc", "acct"); !ok || v != "new-token" {
		t.Errorf("keychain=%q ok=%v want new-token", v, ok)
	}
}
