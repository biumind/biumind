package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAutoEmptyHome(t *testing.T) {
	a := LoadAuto("")
	if a.Dir != "" || a.IndexPath != "" || a.SystemPrompt() != "" {
		t.Errorf("empty home should yield zero-value AutoMemory; got %+v", a)
	}
}

// LoadAuto with a fresh home — no memory dir yet — still returns a
// resolved Dir/IndexPath so the primer can advertise where memories
// will live. SystemPrompt is non-empty (primer-only).
func TestLoadAutoFreshHome(t *testing.T) {
	home := t.TempDir()
	a := LoadAuto(home)
	wantDir := filepath.Join(home, ".biumind", "memory")
	if a.Dir != wantDir {
		t.Errorf("Dir: got %s, want %s", a.Dir, wantDir)
	}
	if a.IndexPath != filepath.Join(wantDir, "MEMORY.md") {
		t.Errorf("IndexPath wrong: %s", a.IndexPath)
	}
	if a.IndexContent != "" {
		t.Errorf("fresh home shouldn't have index content")
	}
	if a.Exists() {
		t.Errorf("Exists should be false on fresh home")
	}
	prompt := a.SystemPrompt()
	if !strings.Contains(prompt, "Auto-memory") {
		t.Errorf("primer header missing")
	}
	if !strings.Contains(prompt, wantDir) {
		t.Errorf("primer should cite the memory dir; got %q", prompt[:200])
	}
}

func TestLoadAutoReadsIndex(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".biumind", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "- [Lang preference](language.md) — user wants Chinese replies\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a := LoadAuto(home)
	if !a.Exists() {
		t.Errorf("Exists should be true after MEMORY.md is written")
	}
	if !strings.Contains(a.IndexContent, "Chinese replies") {
		t.Errorf("index content not loaded: %q", a.IndexContent)
	}
	prompt := a.SystemPrompt()
	if !strings.Contains(prompt, "Chinese replies") {
		t.Errorf("system prompt should embed index content")
	}
	if !strings.Contains(prompt, "auto-memory index") {
		t.Errorf("section header missing")
	}
}

func TestLoadAutoTruncatesByLines(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".biumind", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 250 lines — exceeds the 200 cap.
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("- entry\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	a := LoadAuto(home)
	if !a.LineTruncated {
		t.Errorf("LineTruncated flag should fire for 250-line index")
	}
	got := strings.Count(a.IndexContent, "\n")
	if got > AutoMemoryMaxLines {
		t.Errorf("index keeps %d newlines, want ≤%d", got, AutoMemoryMaxLines)
	}
	if !strings.Contains(a.SystemPrompt(), "200 lines") {
		t.Errorf("truncation warning should mention the cap")
	}
}

func TestLoadAutoTruncatesByBytes(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".biumind", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One giant line — gets past the line cap (only 1 line) but
	// trips the byte cap.
	body := strings.Repeat("x", AutoMemoryMaxBytes+1000)
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a := LoadAuto(home)
	if !a.ByteTruncated {
		t.Errorf("ByteTruncated should fire for oversized single-line index")
	}
	if len(a.IndexContent) > AutoMemoryMaxBytes {
		t.Errorf("byte cap not enforced: len=%d", len(a.IndexContent))
	}
}

func TestEnsureDirCreatesPath(t *testing.T) {
	home := t.TempDir()
	a := LoadAuto(home)
	if a.Exists() {
		t.Fatalf("setup: Exists should be false")
	}
	got, err := a.EnsureDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != a.Dir {
		t.Errorf("EnsureDir returned %q, want %q", got, a.Dir)
	}
	info, err := os.Stat(got)
	if err != nil || !info.IsDir() {
		t.Errorf("dir not created: err=%v info=%v", err, info)
	}
	// Idempotent — second call should not error.
	if _, err := a.EnsureDir(); err != nil {
		t.Errorf("EnsureDir should be idempotent; got %v", err)
	}
}

func TestEnsureDirEmptyHomeFails(t *testing.T) {
	a := AutoMemory{} // zero-value, no Dir resolved
	if _, err := a.EnsureDir(); err == nil {
		t.Errorf("EnsureDir on zero-value should fail")
	}
}

// Primer should explain all four memory types so the model can
// classify saves correctly.
func TestPrimerCoversAllTypes(t *testing.T) {
	a := LoadAuto(t.TempDir())
	prompt := a.SystemPrompt()
	for _, kind := range []string{"user", "feedback", "project", "reference"} {
		if !strings.Contains(prompt, kind) {
			t.Errorf("primer missing memory type %q", kind)
		}
	}
	for _, must := range []string{"What NOT to save", "When to read", "frontmatter"} {
		if !strings.Contains(prompt, must) {
			t.Errorf("primer missing required section %q", must)
		}
	}
}

func TestParseMemoryTypeAcceptsCanonical(t *testing.T) {
	for _, want := range ValidMemoryTypes() {
		got, ok := ParseMemoryType(string(want))
		if !ok || got != want {
			t.Errorf("ParseMemoryType(%q) = (%q, %v); want (%q, true)", want, got, ok, want)
		}
	}
	// Case + whitespace tolerant (typed by humans, so be lenient).
	if got, ok := ParseMemoryType("  FEEDBACK  "); !ok || got != TypeFeedback {
		t.Errorf("case-insensitive trim should match feedback; got (%q, %v)", got, ok)
	}
	// Typo rejected — we'd rather fail loudly than write a memory
	// with `type: feedbcak` that the model later can't classify.
	if _, ok := ParseMemoryType("feedbcak"); ok {
		t.Errorf("typo should fail")
	}
}

// Append happy path — file written, index seeded, frontmatter has
// the expected fields, body preserved.
func TestAppendWritesFileAndSeedsIndex(t *testing.T) {
	home := t.TempDir()
	a := LoadAuto(home)

	res, err := a.Append(TypeFeedback, "", "",
		"User prefers Chinese replies for non-code messages.")
	if err != nil {
		t.Fatal(err)
	}
	if res.FilePath == "" {
		t.Fatal("FilePath should be set")
	}
	if !strings.HasPrefix(filepath.Base(res.FilePath), "feedback-") {
		t.Errorf("filename should be type-prefixed; got %q", res.FilePath)
	}
	body, err := os.ReadFile(res.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"---", "name:", "description:", "type: feedback",
		"User prefers Chinese replies",
	} {
		if !strings.Contains(string(body), must) {
			t.Errorf("file missing %q: %s", must, body)
		}
	}
	// Index was created with the seed header + pointer bullet.
	idx, err := os.ReadFile(res.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "Auto-memory index") {
		t.Errorf("index missing seed header: %s", idx)
	}
	if !strings.Contains(string(idx), filepath.Base(res.FilePath)) {
		t.Errorf("index missing pointer to %s", res.FilePath)
	}
}

// Successive appends must keep MEMORY.md tidy: each entry exactly
// once, ordered, with no fused lines from missing trailing newlines.
func TestAppendKeepsIndexTidyAcrossWrites(t *testing.T) {
	home := t.TempDir()
	a := LoadAuto(home)

	for i, msg := range []string{"first thing", "second thing", "third thing"} {
		_, err := a.Append(TypeUser, "", "", msg)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	idx, err := os.ReadFile(a.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	// Each entry should land in EXACTLY one bullet line. The message
	// itself appears twice per bullet (`[name](file) — description`,
	// both default to the body's first line) so count `[<msg>](`
	// instead — that anchors to the link bracket and only matches
	// the bullet, not the description tail.
	for _, msg := range []string{"first thing", "second thing", "third thing"} {
		needle := "[" + msg + "]("
		if c := strings.Count(string(idx), needle); c != 1 {
			t.Errorf("entry %q has %d bullet links, want 1", msg, c)
		}
	}
	// No fused lines: every bullet starts with "- [".
	for _, line := range strings.Split(strings.TrimSpace(string(idx)), "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "- [") {
			t.Errorf("malformed bullet line: %q", line)
		}
	}
}

func TestAppendRejectsInvalidType(t *testing.T) {
	a := LoadAuto(t.TempDir())
	_, err := a.Append(MemoryType("garbage"), "", "", "body")
	if err == nil {
		t.Errorf("invalid type should fail")
	}
	if !strings.Contains(err.Error(), "user, feedback, project, reference") {
		t.Errorf("error should list valid types; got %v", err)
	}
}

func TestAppendRejectsEmptyBody(t *testing.T) {
	a := LoadAuto(t.TempDir())
	if _, err := a.Append(TypeUser, "", "", ""); err == nil {
		t.Errorf("empty body should fail")
	}
	if _, err := a.Append(TypeUser, "", "", "   \n  "); err == nil {
		t.Errorf("whitespace-only body should fail")
	}
}

func TestAppendDerivesNameFromFirstLine(t *testing.T) {
	a := LoadAuto(t.TempDir())
	body := "User wants Chinese replies\nWith a follow-up paragraph that should\nnot leak into the slug."
	res, err := a.Append(TypeUser, "", "", body)
	if err != nil {
		t.Fatal(err)
	}
	// Slug should derive from the FIRST line only.
	base := filepath.Base(res.FilePath)
	if !strings.Contains(base, "user-wants-chinese-replies") {
		t.Errorf("slug didn't capture first line: %s", base)
	}
	if strings.Contains(base, "follow") {
		t.Errorf("slug leaked content from later lines: %s", base)
	}
}

func TestAppendAllowsExplicitNameOverride(t *testing.T) {
	a := LoadAuto(t.TempDir())
	res, err := a.Append(TypeUser, "Chinese language pref",
		"User prefers Chinese for prose, English for code identifiers",
		"User wants Chinese replies for non-code messages.")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(res.FilePath)
	// Explicit name + description wins over the auto-derived
	// first-line. Also encoded into the slug.
	if !strings.Contains(string(body), "name: Chinese language pref") {
		t.Errorf("name override missing: %s", body)
	}
	if !strings.Contains(string(body), "description: User prefers Chinese for prose") {
		t.Errorf("description override missing: %s", body)
	}
	if !strings.Contains(filepath.Base(res.FilePath), "chinese-language-pref") {
		t.Errorf("slug should reflect explicit name: %s", res.FilePath)
	}
}

func TestSlugifyShape(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"hello":                  "hello",
		"Hello, World!":          "hello-world",
		"  trim  spaces  ":       "trim-spaces",
		"unicode 重构 stays ascii": "unicode-stays-ascii",
		"a b c d e f g h":        "a-b-c-d-e-f", // 6-segment cap
		"---only-punct---":       "only-punct",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// LoadAuto after Append should immediately surface the new memory
// in the assembled system prompt — the round-trip the model relies
// on to find what it just wrote.
func TestLoadAutoAfterAppendSurfacesEntry(t *testing.T) {
	home := t.TempDir()
	a := LoadAuto(home)
	if _, err := a.Append(TypeReference, "Linear board",
		"INGEST = pipeline bug tracker",
		"INGEST is the Linear project where pipeline bugs live."); err != nil {
		t.Fatal(err)
	}
	// Re-load and confirm the index appears in the system prompt.
	reloaded := LoadAuto(home)
	if !reloaded.Exists() {
		t.Fatalf("Exists should be true after first append")
	}
	if !strings.Contains(reloaded.SystemPrompt(), "INGEST = pipeline bug tracker") {
		t.Errorf("system prompt missing the just-saved hook")
	}
}
