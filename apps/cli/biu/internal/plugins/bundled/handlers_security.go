// security-guard handler — PreToolUse guard against the two
// specific footguns described in the plugin README:
//
//   1. Edit/Write targeting credential paths (~/.ssh, ~/.aws,
//      ~/.gnupg, .env*).
//   2. Edit/Write whose new content carries a hardcoded secret
//      after api_key= / password= / token= / secret=.
//
// Both are common LLM mistakes: typos like "Edit ~/.aws/config"
// when Read was meant, or hardcoding a Bearer token directly into
// source instead of pulling from env.
//
// Block reasons surface the rule that fired in plain text so the
// model can re-plan. The reasons reference biu mechanisms
// (sandbox / env vars / Read tool) so the re-plan goes the right
// direction instead of looping on a wrong guess.

package bundled

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
)

// credentialPathSegments is the relative-path tail set we refuse to
// Edit/Write. We match by suffix so ~/.ssh/id_rsa, /home/x/.ssh,
// /root/.ssh all hit. .env* matches via a separate basename rule.
var credentialPathSegments = []string{
	".ssh",
	".aws",
	".gnupg",
	".gnupg-keys",
	".config/gh", // gh CLI auth tokens
	".docker",    // ~/.docker/config.json contains registry creds
	".kube",      // ~/.kube/config — k8s tokens
	".npmrc",
	".pypirc",
}

// envFileRE matches dotenv-style files. ".env", ".env.local",
// ".env.production" — case-sensitive on purpose, since "ENV.md"
// is not a credential file.
var envFileRE = regexp.MustCompile(`(^|/)\.env(\.[a-zA-Z0-9_-]+)?$`)

// secretKVPatternRE matches assignments of the form
// `<key> = <value>` or `<key>: <value>` where key is one of the
// usual suspects and value is a non-trivial string. The regex
// requires at least 16 chars in the value to avoid flagging
// placeholders like "x" or "TODO".
var secretKVPatternRE = regexp.MustCompile(
	`(?i)\b(api[_-]?key|password|passwd|secret|access[_-]?token|token|bearer|aws[_-]?secret|private[_-]?key)\b\s*[:=]\s*['"]?([A-Za-z0-9+/=_-]{16,})['"]?`,
)

// securityGuardPreTool inspects PreToolUse payloads and returns a
// blocking Decision when it spots one of the two patterns. Other
// tool calls pass through unchanged.
func securityGuardPreTool(ctx context.Context, payload []byte) (hooks.Decision, error) {
	var msg struct {
		ToolName string         `json:"tool_name"`
		Input    map[string]any `json:"input"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return hooks.Decision{}, nil
	}

	switch msg.ToolName {
	case "Edit", "Write", "MultiEdit", "edit", "write":
		// Both checks apply; either one triggers a block.
		if r := credentialPathBlock(msg.Input); r != "" {
			return hooks.Decision{Block: true, Reason: r}, nil
		}
		if r := secretContentBlock(msg.Input); r != "" {
			return hooks.Decision{Block: true, Reason: r}, nil
		}
	}
	return hooks.Decision{}, nil
}

// credentialPathBlock checks the file_path / path argument against
// the credential path block-list. Returns the human-readable block
// reason (with biu-specific guidance) on a hit, "" otherwise.
func credentialPathBlock(input map[string]any) string {
	var path string
	for _, key := range []string{"file_path", "path", "target", "filePath"} {
		if v, ok := input[key].(string); ok && v != "" {
			path = v
			break
		}
	}
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	for _, seg := range credentialPathSegments {
		if pathContainsSegment(clean, seg) {
			return "security-guard: refusing to write to a credential store path (" + seg + "). " +
				"If the user explicitly asked to edit credentials, ask them to confirm + " +
				"add the specific path to settings.json sandbox.fsWriteAllowExtra. " +
				"For reading, use Read instead — it isn't blocked."
		}
	}
	if envFileRE.MatchString(clean) || envFileRE.MatchString(filepath.Base(clean)) {
		return "security-guard: refusing to Edit/Write a .env file. " +
			"Editing dotenv from inside the agent leaks secrets into transcripts. " +
			"Ask the user to edit env files manually, or have them export the var " +
			"and reference $VAR in the code."
	}
	return ""
}

// pathContainsSegment reports whether path contains seg as a literal
// path-component sequence (not just a substring). Avoids false
// positives like "/home/.sshown/x" matching ".ssh", and supports
// multi-segment patterns like ".config/gh" by requiring every part
// of the pattern to appear consecutively in the path.
func pathContainsSegment(path, seg string) bool {
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	segParts := strings.Split(seg, "/")
	if len(segParts) == 0 || len(pathParts) < len(segParts) {
		return false
	}
	for i := 0; i+len(segParts) <= len(pathParts); i++ {
		match := true
		for j, want := range segParts {
			if pathParts[i+j] != want {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// secretContentBlock scans the tool's `content` / `new_string`
// arguments for hardcoded credentials. Conservative — uses a single
// regex with key-name + value-shape requirements. Misses sophisticated
// leaks (entropy-based detection) but doesn't false-positive on
// legitimate test fixtures with short placeholder values.
func secretContentBlock(input map[string]any) string {
	var bodies []string
	for _, key := range []string{"content", "new_string", "new_content", "text"} {
		if v, ok := input[key].(string); ok && v != "" {
			bodies = append(bodies, v)
		}
	}
	for _, body := range bodies {
		if m := secretKVPatternRE.FindStringSubmatch(body); m != nil {
			return "security-guard: refusing to write a hardcoded credential into source. " +
				"Detected `" + m[1] + "` with a 16+ char literal value. " +
				"Use an env var (os.Getenv / process.env) and document the required key in README, " +
				"or ask the user where the secret should live (vault / .env.example)."
		}
	}
	return ""
}
