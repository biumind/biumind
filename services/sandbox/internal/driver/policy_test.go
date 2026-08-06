package driver

import (
	"errors"
	"testing"
)

func TestPolicyResolveImage(t *testing.T) {
	p := Policy{DefaultImage: "alpine:3.20", ImageAllowlist: []string{"python:3.12-slim"}}

	// 空 → 默认。
	if got, err := p.ResolveImage(""); err != nil || got != "alpine:3.20" {
		t.Fatalf("empty → %q,%v want alpine:3.20", got, err)
	}
	// 默认镜像总允许。
	if got, err := p.ResolveImage("alpine:3.20"); err != nil || got != "alpine:3.20" {
		t.Fatalf("default → %q,%v", got, err)
	}
	// 白名单内。
	if got, err := p.ResolveImage("python:3.12-slim"); err != nil || got != "python:3.12-slim" {
		t.Fatalf("allowlisted → %q,%v", got, err)
	}
	// 白名单外 → 拒绝。
	if _, err := p.ResolveImage("evil/cryptominer:latest"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-allowlisted should be ErrInvalid, got %v", err)
	}
}

func TestAssertSandboxPath(t *testing.T) {
	ok := []string{"", "/workspace", "/workspace/sub/dir", "/tmp", "/tmp/x.txt"}
	for _, p := range ok {
		if err := AssertSandboxPath(p); err != nil {
			t.Errorf("AssertSandboxPath(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{
		"etc",                  // 相对
		"../etc",               // 相对 + ..
		"/etc/passwd",          // 越界根
		"/root",                // 越界根
		"/workspace/../etc",    // 遍历逃逸
		"/workspace/../../etc", // 多级逃逸
		"/tmp/../workspace/../etc",
	}
	for _, p := range bad {
		if err := AssertSandboxPath(p); err == nil {
			t.Errorf("AssertSandboxPath(%q) = nil, want error", p)
		}
	}
}
