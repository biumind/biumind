package api

import "testing"

func TestMatchWikilinkInText_HappyPath(t *testing.T) {
	got, ok := matchWikilinkInText("See [[Transformer]] for details.", "transformer")
	if !ok {
		t.Fatal("expected match")
	}
	if got == "" {
		t.Fatal("expected non-empty snippet")
	}
}

func TestMatchWikilinkInText_CaseInsensitive(t *testing.T) {
	_, ok := matchWikilinkInText("Cite [[claude]] usage.", "Claude")
	// Match function expects already-lowercased target.
	if ok {
		t.Fatal("title argument must be pre-lowercased; mixed case should miss")
	}
	_, ok = matchWikilinkInText("Cite [[CLAUDE]] usage.", "claude")
	if !ok {
		t.Fatal("[[CLAUDE]] should match lowercased 'claude'")
	}
}

func TestMatchWikilinkInText_AliasForm(t *testing.T) {
	_, ok := matchWikilinkInText("As shown in [[rnn|RNN]] paper.", "rnn")
	if !ok {
		t.Fatal("[[target|alias]] form should match by target")
	}
}

func TestMatchWikilinkInText_NoMatch(t *testing.T) {
	_, ok := matchWikilinkInText("plain text without any links", "transformer")
	if ok {
		t.Fatal("plain text must not match")
	}
	_, ok = matchWikilinkInText("Has [[other]] but not target.", "transformer")
	if ok {
		t.Fatal("non-matching link must not match")
	}
}

func TestMatchWikilinkInText_FirstHitOnly(t *testing.T) {
	got, ok := matchWikilinkInText("[[a]] then [[b]] then [[a]] again.", "a")
	if !ok {
		t.Fatal("expected match")
	}
	// snippet should center on FIRST hit, not later occurrences.
	if !contains(got, "[[a]]") {
		t.Errorf("snippet %q should contain [[a]]", got)
	}
}

func TestMatchWikilinkInText_EmptyTitle(t *testing.T) {
	_, ok := matchWikilinkInText("[[]] empty target", "")
	if ok {
		t.Fatal("empty title must not match (sentinel)")
	}
}

func TestSnippetAround_Truncation(t *testing.T) {
	long := "abcdefghijklmnop "
	for i := 0; i < 20; i++ {
		long += "abcdefghijklmnop "
	}
	long += "[[X]]"
	for i := 0; i < 20; i++ {
		long += " trailing"
	}
	hit := len(long) - len(" trailing")*20 - len("[[X]]")
	got := snippetAround(long, hit, hit+5)
	if !startsWith(got, "…") || !endsWith(got, "…") {
		t.Errorf("expected truncation ellipses on both ends, got %q", got)
	}
}

// tiny test helpers — avoid importing strings just for these.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
func startsWith(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func endsWith(s, p string) bool   { return len(s) >= len(p) && s[len(s)-len(p):] == p }
