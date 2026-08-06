// Bundled skills shipped inside the biu binary. These are loaded as
// the first layer in Registry.Load — user (~/.biumind/skills) and
// project (<cwd>/.biumind/skills) layers come later and overwrite
// on name collision, so a user can shadow a bundled skill simply by
// creating a same-named directory in either layer.
//
// We embed the markdown directly via go:embed rather than reading
// from disk so a fresh `biu` install has the core skill set
// (`/loop`, `/debug`, `/stuck`, `/verify`, `/simplify`, `/remember`)
// available out of the box without any setup step.
//
// To add a bundled skill: drop a `bundled/<name>/SKILL.md` next to
// this file. The embed directive picks it up at compile time. No
// registration code change needed.

package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

//go:embed bundled/*/SKILL.md
var bundledFS embed.FS

// loadBundled walks the embedded bundled/ directory and registers
// each SKILL.md as source="bundled". Failures are logged but
// non-fatal — a malformed bundled skill should never prevent biu
// from starting.
func (r *Registry) loadBundled() {
	entries, err := fs.ReadDir(bundledFS, "bundled")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[biu] bundled skills: %v\n", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := path.Join("bundled", e.Name(), "SKILL.md")
		raw, err := bundledFS.ReadFile(skillPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[biu] bundled skill %s: %v\n", skillPath, err)
			continue
		}
		s := parseSkillBytes(raw, "bundled", "embed:"+skillPath)
		if s.Name == "" {
			s.Name = e.Name()
		}
		r.byName[s.Name] = s
	}
}

// parseSkillBytes is the in-memory equivalent of loadFile. Shared
// shape so bundled and disk-backed skills go through the same
// frontmatter parser.
func parseSkillBytes(raw []byte, source, identifier string) Skill {
	frontmatter, body := splitFrontmatter(string(raw))
	s := Skill{
		Source: source,
		Path:   identifier,
		Body:   strings.TrimSpace(body),
	}
	for k, v := range frontmatter {
		switch strings.ToLower(k) {
		case "name":
			s.Name = v
		case "description":
			s.Description = v
		case "when-to-use", "whentouse":
			s.WhenToUse = v
		case "user-invocable", "userinvocable":
			s.UserInvocable = parseBool(v)
		case "paths":
			s.Paths = parsePathsFrontmatter(v)
		}
	}
	return s
}
