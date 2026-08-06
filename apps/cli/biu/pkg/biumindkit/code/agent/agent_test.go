package agent

import (
	"strings"
	"testing"
)

func TestBuildClaude_PermissionFlags(t *testing.T) {
	cases := map[string][]string{
		"ask":         {"--permission-mode", "default"},
		"auto_edit":   {"--permission-mode", "acceptEdits"},
		"full_access": {"--dangerously-skip-permissions"},
	}
	for mode, wantFlags := range cases {
		spec, err := BuildLaunch("claude", "/bin/claude", mode, "fix bug", "", "")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		joined := strings.Join(spec.Args, " ")
		for _, f := range wantFlags {
			if !strings.Contains(joined, f) {
				t.Errorf("claude %s: args %v missing %q", mode, spec.Args, f)
			}
		}
		// prompt 作 positional(最后一项)
		if spec.Args[len(spec.Args)-1] != "fix bug" {
			t.Errorf("claude %s: prompt not last positional: %v", mode, spec.Args)
		}
	}
}

func TestBuildClaude_EmptyPromptNoPositional(t *testing.T) {
	spec, _ := BuildLaunch("claude", "/bin/claude", "ask", "", "", "")
	if strings.Contains(strings.Join(spec.Args, " "), "default") == false {
		t.Fatal("expected --permission-mode default")
	}
	// 末项应是 "default"(flag),而非空 prompt
	if spec.Args[len(spec.Args)-1] == "" {
		t.Error("empty prompt should not be appended as positional")
	}
}

func TestBuildCodex_Flags(t *testing.T) {
	auto, _ := BuildLaunch("codex", "/bin/codex", "auto_edit", "do it", "", "")
	j := strings.Join(auto.Args, " ")
	if !strings.Contains(j, "--sandbox workspace-write") || !strings.Contains(j, "-a on-request") {
		t.Errorf("codex auto_edit args wrong: %v", auto.Args)
	}
	// codex prompt 在 -- 之后
	if auto.Args[len(auto.Args)-2] != "--" || auto.Args[len(auto.Args)-1] != "do it" {
		t.Errorf("codex prompt should follow --: %v", auto.Args)
	}

	full, _ := BuildLaunch("codex", "/bin/codex", "full_access", "", "", "")
	if !strings.Contains(strings.Join(full.Args, " "), "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("codex full_access flag wrong: %v", full.Args)
	}
}

func TestBuildLaunch_BiuRejected(t *testing.T) {
	if _, err := BuildLaunch("biu", "/bin/biu", "ask", "x", "", ""); err == nil {
		t.Fatal("biu should be rejected (in-process, not PTY)")
	}
}

func TestBuildClaude_SessionID(t *testing.T) {
	// 传 sessionID → 出现 --session-id <sid>,且 prompt 仍是末位 positional。
	spec, _ := BuildLaunch("claude", "/bin/claude", "auto_edit", "go", "sid-123", "")
	j := strings.Join(spec.Args, " ")
	if !strings.Contains(j, "--session-id sid-123") {
		t.Errorf("expected --session-id sid-123 in %v", spec.Args)
	}
	if spec.Args[len(spec.Args)-1] != "go" {
		t.Errorf("prompt should remain last positional: %v", spec.Args)
	}
	// 不传 → 无 --session-id
	none, _ := BuildLaunch("claude", "/bin/claude", "ask", "go", "", "")
	if strings.Contains(strings.Join(none.Args, " "), "--session-id") {
		t.Errorf("no sessionID should omit --session-id: %v", none.Args)
	}
	// codex 忽略 sessionID(用落盘发现)
	cdx, _ := BuildLaunch("codex", "/bin/codex", "auto_edit", "go", "sid-xyz", "")
	if strings.Contains(strings.Join(cdx.Args, " "), "--session-id") {
		t.Errorf("codex should ignore sessionID: %v", cdx.Args)
	}
}

func TestParseLeadingSemver(t *testing.T) {
	cases := map[string]struct {
		want [3]int
		ok   bool
	}{
		"2.1.185 (Claude Code)": {[3]int{2, 1, 185}, true},
		"2.1.87":                {[3]int{2, 1, 87}, true},
		"10.20.30\n":            {[3]int{10, 20, 30}, true},
		"v2.1.0":                {[3]int{}, false}, // 前导非数字
		"2.1":                   {[3]int{}, false}, // 缺 patch
		"":                      {[3]int{}, false},
	}
	for in, exp := range cases {
		got, ok := parseLeadingSemver(in)
		if ok != exp.ok || (ok && got != exp.want) {
			t.Errorf("parseLeadingSemver(%q) = %v,%v; want %v,%v", in, got, ok, exp.want, exp.ok)
		}
	}
}

func TestVersionGTE(t *testing.T) {
	min := [3]int{2, 1, 87}
	yes := [][3]int{{2, 1, 87}, {2, 1, 185}, {2, 2, 0}, {3, 0, 0}}
	no := [][3]int{{2, 1, 86}, {2, 0, 99}, {1, 9, 9}}
	for _, v := range yes {
		if !versionGTE(v, min) {
			t.Errorf("versionGTE(%v, %v) should be true", v, min)
		}
	}
	for _, v := range no {
		if versionGTE(v, min) {
			t.Errorf("versionGTE(%v, %v) should be false", v, min)
		}
	}
}

func TestDetectPath_UnknownAgent(t *testing.T) {
	if _, err := DetectPath("bogus"); err == nil {
		t.Fatal("unknown agent should error")
	}
}

func TestBuildLaunch_ModelFlag(t *testing.T) {
	// claude:--model 在 --session-id / prompt 之前。
	cl, _ := BuildLaunch("claude", "/bin/claude", "ask", "go", "sid-1", "claude-opus-4-8")
	if !containsPair(cl.Args, "--model", "claude-opus-4-8") {
		t.Errorf("claude args missing --model: %v", cl.Args)
	}
	// codex:--model 在 `--` 之前。
	cx, _ := BuildLaunch("codex", "/bin/codex", "auto_edit", "go", "", "gpt-5-codex")
	if !containsPair(cx.Args, "--model", "gpt-5-codex") {
		t.Errorf("codex args missing --model: %v", cx.Args)
	}
	mi := indexOf(cx.Args, "--model")
	di := indexOf(cx.Args, "--")
	if di >= 0 && mi >= 0 && mi > di {
		t.Errorf("codex --model must precede --: %v", cx.Args)
	}
	// 空 model → 不加 --model。
	none, _ := BuildLaunch("claude", "/bin/claude", "ask", "go", "", "")
	if indexOf(none.Args, "--model") >= 0 {
		t.Errorf("empty model should not add --model: %v", none.Args)
	}
}

func containsPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

func TestBuildResume_Claude(t *testing.T) {
	spec, err := BuildResume("claude", "/bin/claude", "auto_edit", "sid-abc", "")
	if err != nil {
		t.Fatal(err)
	}
	j := strings.Join(spec.Args, " ")
	if !strings.Contains(j, "--permission-mode acceptEdits") {
		t.Errorf("resume should keep perm flags: %v", spec.Args)
	}
	if !strings.Contains(j, "--resume sid-abc") {
		t.Errorf("expected --resume sid-abc: %v", spec.Args)
	}
	// resume 不带 prompt(末项是 sessionID)
	if spec.Args[len(spec.Args)-1] != "sid-abc" {
		t.Errorf("resume sessionID should be last: %v", spec.Args)
	}
}

func TestBuildResume_Codex(t *testing.T) {
	spec, err := BuildResume("codex", "/bin/codex", "full_access", "sid-xyz", "")
	if err != nil {
		t.Fatal(err)
	}
	j := strings.Join(spec.Args, " ")
	if !strings.Contains(j, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("codex resume should keep perm flags: %v", spec.Args)
	}
	if !strings.Contains(j, "resume sid-xyz") {
		t.Errorf("expected resume sid-xyz: %v", spec.Args)
	}
}
