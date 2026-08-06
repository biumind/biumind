package mcp

import (
	"os"
	"reflect"
	"testing"
)

func TestNormalizeToolName(t *testing.T) {
	cases := map[string]string{
		"file_read":     "file_read",
		"file.read":     "file_read",
		"repo:list":     "repo_list",
		"create-pr":     "create-pr",
		"a.b.c":         "a_b_c",
		"weird name":    "weird_name",
		"Already_Good":  "Already_Good",
		"":              "tool",
		"___leading":    "leading",
		"trailing___":   "trailing",
		"double__under": "double_under",
		"中文工具":          "tool", // all stripped → fallback
	}
	for in, want := range cases {
		if got := NormalizeToolName(in); got != want {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandEnvVarsBasic(t *testing.T) {
	t.Setenv("FOO", "bar")
	exp, miss := ExpandEnvVars("${FOO}-baz")
	if exp != "bar-baz" || len(miss) != 0 {
		t.Errorf("got %q miss=%v", exp, miss)
	}
}

func TestExpandEnvVarsDefault(t *testing.T) {
	os.Unsetenv("MISSING_KEY_X")
	exp, miss := ExpandEnvVars("${MISSING_KEY_X:-default-val}")
	if exp != "default-val" {
		t.Errorf("default not used: %q", exp)
	}
	if len(miss) != 0 {
		t.Errorf("default-supplied vars should NOT report missing: %v", miss)
	}
}

func TestExpandEnvVarsMissing(t *testing.T) {
	os.Unsetenv("MISSING_KEY_Y")
	exp, miss := ExpandEnvVars("token=${MISSING_KEY_Y}")
	if exp != "token=${MISSING_KEY_Y}" {
		t.Errorf("missing var without default should leave literal: %q", exp)
	}
	if len(miss) != 1 || miss[0] != "MISSING_KEY_Y" {
		t.Errorf("missing list: %v", miss)
	}
}

func TestExpandEnvVarsMultiple(t *testing.T) {
	t.Setenv("A", "1")
	t.Setenv("B", "2")
	exp, _ := ExpandEnvVars("${A}-${B}-${A}")
	if exp != "1-2-1" {
		t.Errorf("got %q", exp)
	}
}

func TestSignatureFor(t *testing.T) {
	a := SignatureFor("docker", []string{"run", "-i", "image"})
	b := SignatureFor("docker", []string{"run", "-i", "image"})
	c := SignatureFor("docker", []string{"run", "image"})
	if a != b {
		t.Errorf("identical configs got different sigs")
	}
	if a == c {
		t.Errorf("different args should differ")
	}
}

func TestNormalizeServerName(t *testing.T) {
	cases := map[string]string{
		"github":       "github",
		"GitHub":       "github",
		"my server":    "my_server",
		"weird/slash":  "weird_slash",
		"_-leading-_":  "leading",
		"中文":           "",
	}
	for in, want := range cases {
		if got := NormalizeServerName(in); got != want {
			t.Errorf("NormalizeServerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandEnvMap(t *testing.T) {
	t.Setenv("TOKEN", "secret")
	in := map[string]string{
		"GH_TOKEN": "${TOKEN}",
		"FALLBACK": "${UNSET_X:-fallback}",
	}
	out, miss := ExpandEnvMap(in)
	want := map[string]string{
		"GH_TOKEN": "secret",
		"FALLBACK": "fallback",
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
	if len(miss) != 0 {
		t.Errorf("expected no missing, got %v", miss)
	}
}
