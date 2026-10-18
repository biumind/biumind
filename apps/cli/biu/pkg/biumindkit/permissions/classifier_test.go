package permissions

import "testing"

func TestMatchPromptToCommand(t *testing.T) {
	cases := []struct {
		name    string
		prompt  string
		command string
		want    bool
	}{
		// Single-keyword prompts — most common shape.
		{"runs tests", "run tests", "go test ./...", true},
		{"deploy keyword", "deploy", "git push deploy main", true},
		{"miss returns false", "run tests", "npm install foo", false},
		// Prefix stem catches plural / inflected forms.
		{"plural via stem", "tests", "go test ./...", true},
		{"npm test from prompt", "test", "npm test", true},
		// All keywords must hit — partial match is rejected. The
		// model is expected to use words that WILL appear in the
		// command; vague / abstract prompts are deliberately strict
		// so the user falls back to the per-call ask path.
		{"strict on multi-keyword: install matches", "install", "npm install --save-dev foo", true},
		{"strict on multi-keyword: deps fails", "install dependencies", "npm install --save-dev foo", false},
		{"docker keyword anchors specific match", "docker build", "docker build -t app .", true},
		// Case insensitivity.
		{"case insensitive", "RUN TESTS", "Go Test ./...", true},
		// Edge / safety cases.
		{"empty prompt rejected", "", "go test", false},
		{"empty command rejected", "run tests", "", false},
		{"all stop words rejected", "run all the", "go test ./...", false},
		{"hyphen flags survive tokenisation", "lint --fix", "biome check --fix .", false}, // "lint" missing
		{"hyphen flag fully matched", "biome --fix", "biome check --fix .", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchPromptToCommand(c.prompt, c.command)
			if got != c.want {
				t.Errorf("matchPromptToCommand(%q, %q) = %v, want %v", c.prompt, c.command, got, c.want)
			}
		})
	}
}

func TestStemmedKeywordsDropsDigits(t *testing.T) {
	got := stemmedKeywords("step 1 install deps 42")
	for _, k := range got {
		if k == "1" || k == "42" {
			t.Errorf("digit-only token should be dropped; got %v", got)
		}
	}
	if len(got) != 3 { // "step", "inst", "deps"
		t.Errorf("want 3 keywords, got %v", got)
	}
}
