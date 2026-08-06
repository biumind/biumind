package hooks

import (
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestInjectCodex_AppendThenIdempotent(t *testing.T) {
	user := "model = \"o3\"\n\n[tui]\ntheme = \"dark\"\n"
	once := injectCodexText(user, "/home/u/.biu/hooks/biu-hook.mjs")

	// 用户原内容必须完整保留。
	if !strings.Contains(once, "model = \"o3\"") || !strings.Contains(once, "theme = \"dark\"") {
		t.Fatalf("user content lost after inject:\n%s", once)
	}
	// 注入块在,且整体仍是合法 TOML。
	if !strings.Contains(once, codexBegin) || !strings.Contains(once, codexEnd) {
		t.Fatalf("marker block missing:\n%s", once)
	}
	var probe map[string]any
	if err := toml.Unmarshal([]byte(once), &probe); err != nil {
		t.Fatalf("injected TOML invalid: %v\n%s", err, once)
	}

	// 二次注入幂等:marker 区域被整段替换,不重复堆叠。
	twice := injectCodexText(once, "/home/u/.biu/hooks/biu-hook.mjs")
	if strings.Count(twice, codexBegin) != 1 || strings.Count(twice, codexEnd) != 1 {
		t.Fatalf("re-inject should not stack markers:\n%s", twice)
	}
}

func TestUninjectCodex_RestoresUserContent(t *testing.T) {
	user := "model = \"o3\"\n[tui]\ntheme = \"dark\"\n"
	injected := injectCodexText(user, "/x/biu-hook.mjs")
	restored := uninjectCodexText(injected)

	if strings.Contains(restored, codexBegin) || strings.Contains(restored, codexEnd) {
		t.Fatalf("markers should be gone after uninject:\n%s", restored)
	}
	if !strings.Contains(restored, "model = \"o3\"") || !strings.Contains(restored, "theme = \"dark\"") {
		t.Fatalf("user content not preserved after uninject:\n%s", restored)
	}
	// 卸载后仍合法 TOML。
	var probe map[string]any
	if err := toml.Unmarshal([]byte(restored), &probe); err != nil {
		t.Fatalf("restored TOML invalid: %v\n%s", err, restored)
	}
}

func TestUninjectCodex_NoMarkerNoOp(t *testing.T) {
	user := "model = \"o3\"\n"
	if got := uninjectCodexText(user); got != user {
		t.Fatalf("no-marker uninject should be no-op, got:\n%s", got)
	}
}

func TestBuildClaudeSettings_AllEvents(t *testing.T) {
	raw, err := buildClaudeSettings("/x/biu-hook.mjs")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, ev := range claudeEvents {
		if !strings.Contains(s, "\""+ev+"\"") {
			t.Errorf("claude settings missing event %q:\n%s", ev, s)
		}
	}
	if !strings.Contains(s, "node ") || !strings.Contains(s, "biu-hook.mjs") {
		t.Errorf("hook command malformed:\n%s", s)
	}
}

func TestTomlQuote_EscapesBackslashAndQuote(t *testing.T) {
	got := tomlQuote(`node "C:\x\biu.mjs"`)
	// 反斜杠与引号都要转义,结果本身可被 TOML 解析回来。
	var probe map[string]any
	if err := toml.Unmarshal([]byte("command = "+got), &probe); err != nil {
		t.Fatalf("tomlQuote output not parseable: %v (%s)", err, got)
	}
	if probe["command"] != `node "C:\x\biu.mjs"` {
		t.Fatalf("round-trip mismatch: %v", probe["command"])
	}
}

func TestHookCommand_BareNode(t *testing.T) {
	if got := hookCommand("/a b/biu-hook.mjs"); got != `node "/a b/biu-hook.mjs"` {
		t.Fatalf("hookCommand = %q", got)
	}
}
