package agent

import (
	"context"
	"testing"
)

func TestDetectBinaryName(t *testing.T) {
	cases := map[string]string{
		"claude": "claude",
		"codex":  "codex",
		"biu":    "biu",
		"foo":    "",
		"":       "",
	}
	for in, want := range cases {
		if got := detectBinaryName(in); got != want {
			t.Errorf("detectBinaryName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDetect_UnknownType(t *testing.T) {
	r := Detect(context.Background(), "nope")
	if r.Found || r.Path != "" {
		t.Errorf("unknown type should be not-found: %+v", r)
	}
}

func TestResolveBinaryPath_KnownAndMissing(t *testing.T) {
	// sh 在所有 unix CI 上都在 PATH 里。
	if p := resolveBinaryPath("sh"); p == "" {
		t.Error("expected to resolve 'sh' on PATH")
	}
	// 几乎不可能存在的名字 → 空。
	if p := resolveBinaryPath("biumind-no-such-binary-xyz"); p != "" {
		t.Errorf("expected empty for missing binary, got %q", p)
	}
}

func TestDetectAll_Shape(t *testing.T) {
	all := DetectAll(context.Background())
	for _, k := range []string{"claude", "codex", "biu"} {
		if _, ok := all[k]; !ok {
			t.Errorf("DetectAll missing key %q", k)
		}
	}
}
