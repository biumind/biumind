package permissions

import "testing"

func TestSensitivePath(t *testing.T) {
	sensitive := []string{
		".env", ".env.local", "/app/.env.production",
		"/home/u/.ssh/id_rsa", "id_ed25519",
		"cert.pem", "server.key", "store.jks", "keys.p12",
		"/repo/.git/config", ".git/HEAD",
		"/home/u/.aws/credentials", "/home/u/.gnupg/secring.gpg",
		"/home/u/.zshrc", ".bashrc", ".profile",
		"C:\\Users\\u\\.env",
	}
	for _, p := range sensitive {
		if !isSensitivePath(p) {
			t.Errorf("expected sensitive: %q", p)
		}
	}
	inert := []string{
		"", "main.go", "src/env.go", "environment.md",
		"docs/pemmican.md", "monkey.txt", ".github/workflows/ci.yml",
		"gitops/readme.md", "src/credentials.go",
	}
	for _, p := range inert {
		if isSensitivePath(p) {
			t.Errorf("expected inert: %q", p)
		}
	}
}

func TestAutoModeDangerousCommandGuardAsks(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModeAuto)
	req := Request{
		Tool:          "Bash",
		IsDestructive: true,
		Args:          map[string]any{"command": "rm -rf /tmp/x"},
		Warnings:      []string{"may recursively force-remove files"},
	}
	d, reason := Decide(ctx, req)
	if d != DecideAsk {
		t.Fatalf("guard should escalate warned call to ask; got %v", d)
	}
	if reason.Kind != "guard" {
		t.Errorf("expected guard reason, got %q", reason.Kind)
	}
}

func TestAutoModeGuardDisabledApproves(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModeAuto)
	ctx.SetGuardsEnabled(false)
	req := Request{
		Tool:          "Bash",
		IsDestructive: true,
		Args:          map[string]any{"command": "rm -rf /tmp/x"},
		Warnings:      []string{"may recursively force-remove files"},
	}
	if d, _ := Decide(ctx, req); d != DecideAllow {
		t.Errorf("guards disabled → auto approves; got %v", d)
	}
}

func TestGuardOnlyFiresInAutoMode(t *testing.T) {
	ctx := NewContext() // default mode
	req := Request{
		Tool:          "Bash",
		IsDestructive: true,
		Args:          map[string]any{"command": "rm -rf /tmp/x"},
		Warnings:      []string{"may recursively force-remove files"},
	}
	d, reason := Decide(ctx, req)
	if d != DecideAsk {
		t.Fatalf("default mode still asks; got %v", d)
	}
	if reason.Kind == "guard" {
		t.Error("guard must not claim the decision outside auto mode")
	}
}

func TestAutoModeSensitivePathGuardAsks(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModeAuto)
	d, reason := Decide(ctx, Request{
		Tool:       "Read",
		IsReadOnly: true,
		Args:       map[string]any{"file_path": "/app/.env"},
	})
	if d != DecideAsk {
		t.Fatalf("sensitive read in auto mode should ask; got %v", d)
	}
	if reason.Kind != "guard" {
		t.Errorf("expected guard reason, got %q", reason.Kind)
	}
	// Ordinary path still sails through read-only allow.
	d, _ = Decide(ctx, Request{
		Tool:       "Read",
		IsReadOnly: true,
		Args:       map[string]any{"file_path": "/app/main.go"},
	})
	if d != DecideAllow {
		t.Errorf("ordinary read should auto-allow; got %v", d)
	}
}
