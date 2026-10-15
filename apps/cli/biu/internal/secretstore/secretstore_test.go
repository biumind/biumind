package secretstore

import (
	"os"
	"path/filepath"
	"testing"
)

// 纯文件矩阵：Open 只持路径，Get/Set/Delete 全部落 0600 文件。
func TestSecretStore_FileRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "secret")
	s := Open(file)
	if s.Backend() != "file" {
		t.Fatalf("backend=%q want file", s.Backend())
	}
	if s.Path() != file {
		t.Errorf("path=%q want %q", s.Path(), file)
	}

	// 缺失 → 空串无错误。
	if v, err := s.Get(); err != nil || v != "" {
		t.Errorf("missing Get=%q err=%v, want \"\"", v, err)
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
	// 二次 Delete 不报错。
	if err := s.Delete(); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}

// 空路径（调用方取不到 home 时的降级）读为空、不 panic。
func TestSecretStore_EmptyPath(t *testing.T) {
	s := Open("")
	if v, err := s.Get(); err != nil || v != "" {
		t.Errorf("empty-path Get=%q err=%v, want \"\"", v, err)
	}
}
