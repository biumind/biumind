package permissions

import "testing"

func TestFullAccessAllowsAll(t *testing.T) {
	p := New(ModeFullAccess, nil)
	d, _ := p.Evaluate(Request{Tool: "bash", IsDestructive: true, Args: map[string]any{"cmd": "rm -rf /"}})
	if d != DecideAllow {
		t.Errorf("got %v", d)
	}
}

func TestReadOnlyAllowed(t *testing.T) {
	p := New(ModeAsk, nil)
	d, _ := p.Evaluate(Request{Tool: "read", IsReadOnly: true, Args: map[string]any{"path": "main.go"}})
	if d != DecideAllow {
		t.Errorf("got %v", d)
	}
}

func TestAllowlistCommand(t *testing.T) {
	p := New(ModeAsk, []string{"bash:ls", "bash:git status"})
	d, _ := p.Evaluate(Request{Tool: "bash", Args: map[string]any{"cmd": "ls -la"}})
	if d != DecideAllow {
		t.Errorf("ls allowlist failed; got %v", d)
	}
	d, _ = p.Evaluate(Request{Tool: "bash", Args: map[string]any{"cmd": "git status"}})
	if d != DecideAllow {
		t.Errorf("git status allowlist failed; got %v", d)
	}
	d, _ = p.Evaluate(Request{Tool: "bash", Args: map[string]any{"cmd": "rm -rf /"}})
	if d != DecideAsk {
		t.Errorf("rm should not auto-allow; got %v", d)
	}
}

func TestAutoEditDestructiveAsks(t *testing.T) {
	p := New(ModeAutoEdit, nil)
	d, _ := p.Evaluate(Request{Tool: "bash", IsDestructive: true, Args: map[string]any{"cmd": "rm -rf x"}})
	if d != DecideAsk {
		t.Errorf("got %v", d)
	}
	d, _ = p.Evaluate(Request{Tool: "edit", Args: map[string]any{"path": "x.go"}})
	if d != DecideAllow {
		t.Errorf("edit non-destructive should auto-allow; got %v", d)
	}
}
