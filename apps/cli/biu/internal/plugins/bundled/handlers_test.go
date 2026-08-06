// Tests for the three Go-implemented bundled hook handlers
// (hookify / ralph-loop / security-guard). Each handler is a pure
// function over (ctx, payload) so we exercise it directly rather
// than going through the runner — that's covered by hooks/runner.

package bundled

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── hookify ────────────────────────────────────────────────

func TestHookify_extractRulesPicksNegations(t *testing.T) {
	body := "# rules\n\n" +
		"- don't run bun install — use pnpm\n" +
		"- DO NOT push to main directly\n" +
		"- never rebase shared branches\n" +
		"- avoid `git rebase -i` in shared branches\n" +
		"- this is just a normal bullet\n"
	got := extractRules(body)
	if len(got) != 4 {
		t.Fatalf("want 4 rules, got %d: %+v", len(got), got)
	}
	wantSubstrings := []string{"bun install", "main directly", "rebase shared", "git rebase"}
	for i, r := range got {
		if !strings.Contains(r.Body, wantSubstrings[i]) {
			t.Errorf("rule %d body mismatch: %q", i, r.Body)
		}
		if len(r.Tokens) == 0 {
			t.Errorf("rule %d has no tokens", i)
		}
	}
}

func TestHookify_extractRulesSkipsEmptyMatcher(t *testing.T) {
	// "don't" with no body produces no significant tokens — should
	// be dropped so the rule doesn't block every tool call.
	body := `- don't.`
	got := extractRules(body)
	if len(got) != 0 {
		t.Errorf("want empty (no significant tokens), got %+v", got)
	}
}

func TestHookify_significantTokensFilters(t *testing.T) {
	got := significantTokens("the bun install command, please")
	// 2-char floor (so CLI names like rm/cp count) plus stop-word
	// filter ("the", "use", "and"). Punctuation must be stripped.
	for _, tok := range got {
		if len(tok) < 2 {
			t.Errorf("token %q below 2-char floor", tok)
		}
		if hookifyStopWords[tok] {
			t.Errorf("token %q is a stop word and should have been filtered", tok)
		}
		if strings.ContainsAny(tok, ",;!?'\"") {
			t.Errorf("token %q has punctuation", tok)
		}
	}
	// Sanity: "bun" should be present (2-char floor lets it through),
	// "the" should not (stop word).
	have := func(want string) bool {
		for _, t := range got {
			if t == want {
				return true
			}
		}
		return false
	}
	if !have("bun") {
		t.Errorf("expected 'bun' in tokens, got %v", got)
	}
	if have("the") {
		t.Errorf("'the' should be filtered, got %v", got)
	}
}

func TestHookify_preToolBlocksMatchedRule(t *testing.T) {
	cwd := pluginsTempCwd(t, ".local.md", "- don't run bun install — use pnpm")
	t.Chdir(cwd)

	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Bash",
		"input":     map[string]any{"command": "bun install --frozen-lockfile"},
	})
	dec, err := hookifyPreTool(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.Block {
		t.Errorf("want Block=true, got %+v", dec)
	}
	if !strings.Contains(dec.Reason, "bun install") {
		t.Errorf("reason should echo the rule, got %q", dec.Reason)
	}
}

func TestHookify_preToolPassesUnmatched(t *testing.T) {
	cwd := pluginsTempCwd(t, ".local.md", "- don't run bun install")
	t.Chdir(cwd)

	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Bash",
		"input":     map[string]any{"command": "pnpm install"},
	})
	dec, err := hookifyPreTool(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Block {
		t.Errorf("clean call shouldn't block, got %+v", dec)
	}
}

func TestHookify_preToolNoOpWithoutRules(t *testing.T) {
	t.Chdir(t.TempDir())
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Bash",
		"input":     map[string]any{"command": "anything"},
	})
	dec, _ := hookifyPreTool(context.Background(), payload)
	if dec.Block || dec.Reason != "" {
		t.Errorf("no rules → no opinion, got %+v", dec)
	}
}

func TestHookify_userPromptAppendsRules(t *testing.T) {
	cwd := pluginsTempCwd(t, ".local.md",
		"- don't push to main\n- avoid `git rebase -i`")
	t.Chdir(cwd)

	dec, err := hookifyUserPrompt(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dec.AdditionalContext, "push to main") {
		t.Errorf("AdditionalContext missing rule: %q", dec.AdditionalContext)
	}
	if !strings.Contains(dec.AdditionalContext, "git rebase") {
		t.Errorf("AdditionalContext missing 2nd rule: %q", dec.AdditionalContext)
	}
}

// ─── ralph-loop ─────────────────────────────────────────────

func TestRalphLoop_replaysGoalWhenDoneAbsent(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ralphGoalFile),
		[]byte("port the next legacy file"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	dec, err := ralphLoopStop(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if dec.ReplacePrompt != "port the next legacy file" {
		t.Errorf("ReplacePrompt = %q", dec.ReplacePrompt)
	}
}

func TestRalphLoop_terminatesWhenDonePresent(t *testing.T) {
	cwd := t.TempDir()
	for _, f := range []string{ralphGoalFile, ralphDoneFile} {
		if err := os.WriteFile(filepath.Join(cwd, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)

	dec, _ := ralphLoopStop(context.Background(), nil)
	if dec.ReplacePrompt != "" {
		t.Errorf("done file present → no replay, got %q", dec.ReplacePrompt)
	}
}

func TestRalphLoop_noGoalFileIsSilentNoOp(t *testing.T) {
	t.Chdir(t.TempDir())
	dec, err := ralphLoopStop(context.Background(), nil)
	if err != nil {
		t.Errorf("missing goal must NOT error: %v", err)
	}
	if dec.ReplacePrompt != "" {
		t.Errorf("missing goal → no opinion, got %q", dec.ReplacePrompt)
	}
}

func TestRalphLoop_emptyGoalIsNoOp(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ralphGoalFile), []byte("   \n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	dec, _ := ralphLoopStop(context.Background(), nil)
	if dec.ReplacePrompt != "" {
		t.Errorf("empty goal should not loop, got %q", dec.ReplacePrompt)
	}
}

// ─── security-guard ─────────────────────────────────────────

func TestSecurityGuard_blocksCredentialPaths(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/me/.ssh/id_rsa", true},
		{"/home/x/.aws/credentials", true},
		{"~/.gnupg/pubring.gpg", true},
		{"~/.config/gh/hosts.yml", true},
		{".env", true},
		{".env.local", true},
		{".env.production", true},
		{"src/main.go", false},
		{"docs/.env-example.md", false}, // basename isn't .env*
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{
				"tool_name": "Edit",
				"input":     map[string]any{"file_path": tc.path, "new_string": "x"},
			})
			dec, _ := securityGuardPreTool(context.Background(), payload)
			if dec.Block != tc.want {
				t.Errorf("Block = %v, want %v (reason: %q)", dec.Block, tc.want, dec.Reason)
			}
		})
	}
}

func TestSecurityGuard_blocksHardcodedSecret(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Write",
		"input": map[string]any{
			"file_path": "config.go",
			"content":   `const token = "ghp_abcdefghijklmnop1234567890"`,
		},
	})
	dec, _ := securityGuardPreTool(context.Background(), payload)
	if !dec.Block {
		t.Errorf("hardcoded token should block, got %+v", dec)
	}
}

func TestSecurityGuard_doesNotBlockShortPlaceholders(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Write",
		"input": map[string]any{
			"file_path": "config.go",
			"content":   `const token = "x"`, // too short to be real
		},
	})
	dec, _ := securityGuardPreTool(context.Background(), payload)
	if dec.Block {
		t.Errorf("short placeholder shouldn't block, got reason %q", dec.Reason)
	}
}

func TestSecurityGuard_passesNonMatchingTools(t *testing.T) {
	// Bash / Read / Grep don't trip the path block — only the
	// edit-shaped tools do.
	for _, tool := range []string{"Bash", "Read", "Grep", "Glob"} {
		payload, _ := json.Marshal(map[string]any{
			"tool_name": tool,
			"input":     map[string]any{"file_path": "/Users/me/.ssh/id_rsa"},
		})
		dec, _ := securityGuardPreTool(context.Background(), payload)
		if dec.Block {
			t.Errorf("tool %q should not be blocked even on credential path", tool)
		}
	}
}

func TestSecurityGuard_pathSegmentNotSubstring(t *testing.T) {
	// Regression: ".sshown/x" should NOT match ".ssh".
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Edit",
		"input":     map[string]any{"file_path": "/tmp/.sshown/x.go"},
	})
	dec, _ := securityGuardPreTool(context.Background(), payload)
	if dec.Block {
		t.Errorf("partial-name match shouldn't block, got %q", dec.Reason)
	}
}

func TestSecurityGuard_badPayloadIsSilent(t *testing.T) {
	dec, _ := securityGuardPreTool(context.Background(), []byte("not json"))
	if dec.Block {
		t.Error("malformed payload should pass through, not block")
	}
}

// ─── helpers ────────────────────────────────────────────────

// pluginsTempCwd creates a tempdir + writes one file + returns the
// dir. Used so each handler test gets a clean, isolated working
// directory for its hook to inspect.
func pluginsTempCwd(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
