package repoapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnv(t *testing.T) {
	data := []byte(`
# comment line
FOO=bar
export EXPORTED=1
QUOTED="hello world"
SINGLE='sq'
EMPTY=
MALFORMED LINE WITHOUT EQUALS
SPACED = value with spaces
`)
	env := ParseEnv(data)
	want := map[string]string{
		"FOO":      "bar",
		"EXPORTED": "1",
		"QUOTED":   "hello world",
		"SINGLE":   "sq",
		"EMPTY":    "",
		"SPACED":   "value with spaces",
	}
	if len(env) != len(want) {
		t.Errorf("ParseEnv parsed %d keys want %d: %v", len(env), len(want), env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%q] = %q want %q", k, env[k], v)
		}
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	env, err := LoadEnvFile(filepath.Join(t.TempDir(), ".env"))
	if err != nil || len(env) != 0 {
		t.Errorf("missing .env should yield empty map, got %v err=%v", env, err)
	}
}

func TestWriteEnvFilePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := WriteEnvFile(path, map[string]string{"SECRET": "s3cr3t"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %o want 600", info.Mode().Perm())
	}
	env, err := LoadEnvFile(path)
	if err != nil || env["SECRET"] != "s3cr3t" {
		t.Errorf("roundtrip failed: %v err=%v", env, err)
	}
}
