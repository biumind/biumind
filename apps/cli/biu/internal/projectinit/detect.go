// Package projectinit detects a project's languages / package manager
// / common commands from disk and renders a starter BIUMIND.md.
//
// Deterministic (no LLM call), fast (just stats a handful of well-
// known manifest files), and conservative — when in doubt we leave
// a placeholder rather than guess. The whole point is to give the
// user a useful first draft they can edit, NOT a definitive doc.
//
// Detection covers the same set of well-known manifest files with a
// "detect then fill in" structure, but skips the interactive
// sub-agent dispatch. The user gets a deterministic file in <100ms;
// they can edit by hand or re-run `/init --regen` after they've
// evolved the project.

package projectinit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Language is a recognised technology stack the detector classifies.
// Values appear verbatim in the rendered BIUMIND.md headers so they
// double as user-facing labels.
type Language string

const (
	LangGo     Language = "Go"
	LangNode   Language = "Node"
	LangPython Language = "Python"
	LangRust   Language = "Rust"
	LangJava   Language = "Java"
	LangRuby   Language = "Ruby"
	LangPHP    Language = "PHP"
)

// PackageManager is the resolved tool used to invoke commands for
// the language. Set when detection narrows it down (e.g. pnpm vs
// npm via lockfile presence); blank means "use the language's
// default" (e.g. plain `go test`).
type PackageManager string

const (
	PMNpm    PackageManager = "npm"
	PMPnpm   PackageManager = "pnpm"
	PMYarn   PackageManager = "yarn"
	PMBun    PackageManager = "bun"
	PMUv     PackageManager = "uv"
	PMPoetry PackageManager = "poetry"
	PMPip    PackageManager = "pip"
	PMCargo  PackageManager = "cargo"
	PMGoMod  PackageManager = "go"
	PMMaven  PackageManager = "maven"
	PMGradle PackageManager = "gradle"
)

// LangSection is one detected language's commands + manager. The
// renderer prints one section per LangSection, in detection order.
type LangSection struct {
	Language Language
	Manager  PackageManager
	// Commands is keyed by intent ("build" / "test" / "race" /
	// "lint" / "format" / "run") so the renderer can group them
	// stably. Empty value = the intent isn't applicable for this
	// stack.
	Commands map[string]string
}

// DetectedProject is the full snapshot returned by Detect. Empty
// Languages = nothing recognised; the renderer falls back to a
// minimal generic template.
type DetectedProject struct {
	Cwd       string
	Languages []LangSection

	// Notes are non-blocking observations the renderer surfaces
	// at the bottom of the doc — "found .cursor/rules", "monorepo
	// with pnpm workspaces", "no test runner detected", etc.
	Notes []string
}

// Detect classifies `cwd` by scanning a fixed set of manifest files.
// All checks are file-stat or one-shot reads — no shelling out, no
// recursive directory walks beyond a single level — so the call
// returns in ~milliseconds even on large repos.
func Detect(cwd string) DetectedProject {
	d := DetectedProject{Cwd: cwd}

	if exists(cwd, "go.mod") {
		d.Languages = append(d.Languages, detectGo(cwd))
	}
	if exists(cwd, "package.json") {
		d.Languages = append(d.Languages, detectNode(cwd))
	}
	if exists(cwd, "Cargo.toml") {
		d.Languages = append(d.Languages, detectRust())
	}
	if exists(cwd, "pyproject.toml") || exists(cwd, "setup.py") || exists(cwd, "requirements.txt") {
		d.Languages = append(d.Languages, detectPython(cwd))
	}
	if exists(cwd, "pom.xml") {
		d.Languages = append(d.Languages, javaSection(PMMaven))
	} else if exists(cwd, "build.gradle") || exists(cwd, "build.gradle.kts") {
		d.Languages = append(d.Languages, javaSection(PMGradle))
	}
	if exists(cwd, "Gemfile") {
		d.Languages = append(d.Languages, rubySection())
	}
	if exists(cwd, "composer.json") {
		d.Languages = append(d.Languages, phpSection())
	}

	d.Notes = append(d.Notes, sniffNotes(cwd)...)
	return d
}

// ─── per-language detectors ─────────────────────────────

func detectGo(cwd string) LangSection {
	cmds := map[string]string{
		"build":  "go build ./...",
		"test":   "go test ./...",
		"race":   "go test -race ./...",
		"vet":    "go vet ./...",
		"format": "gofmt -l -w .",
	}
	// Some projects ship a `tools.go` or `Makefile` with a
	// canonical entry point — surface that as a hint when found.
	if exists(cwd, "Makefile") {
		cmds["make"] = "make"
	}
	return LangSection{
		Language: LangGo,
		Manager:  PMGoMod,
		Commands: cmds,
	}
}

// nodePackageJSON is the subset of `package.json` we parse. Marshal
// failures degrade silently — biu doesn't enforce shape on user
// configs.
type nodePackageJSON struct {
	Scripts map[string]string `json:"scripts"`
	// PackageManager is the npm-standard "packageManager" field
	// (Corepack), e.g. "pnpm@8.6.0". When set we honour it over
	// lockfile heuristics.
	PackageManager string `json:"packageManager"`
}

func detectNode(cwd string) LangSection {
	pm := detectNodePM(cwd)
	cmds := map[string]string{}
	// Read package.json scripts to surface the user's documented
	// commands rather than synthesising. Falls back to the
	// language defaults below when scripts are missing.
	if raw, err := os.ReadFile(filepath.Join(cwd, "package.json")); err == nil {
		var doc nodePackageJSON
		if err := json.Unmarshal(raw, &doc); err == nil {
			for _, intent := range []string{"build", "test", "lint", "format", "typecheck", "dev", "start"} {
				if cmd, ok := doc.Scripts[intent]; ok && strings.TrimSpace(cmd) != "" {
					cmds[intent] = string(pm) + " run " + intent
					_ = cmd // not surfaced — the script body changes too often to cite
				}
			}
		}
	}
	// Fallbacks for stacks that don't define scripts.
	if cmds["test"] == "" {
		cmds["test"] = string(pm) + " test"
	}
	if cmds["build"] == "" && pm == PMBun {
		// Bun's default is no implicit build; leave blank.
	} else if cmds["build"] == "" {
		cmds["build"] = string(pm) + " run build"
	}
	return LangSection{
		Language: LangNode,
		Manager:  pm,
		Commands: cmds,
	}
}

func detectNodePM(cwd string) PackageManager {
	// Corepack-style explicit declaration wins.
	if raw, err := os.ReadFile(filepath.Join(cwd, "package.json")); err == nil {
		var doc nodePackageJSON
		if err := json.Unmarshal(raw, &doc); err == nil {
			pm := strings.ToLower(strings.TrimSpace(doc.PackageManager))
			switch {
			case strings.HasPrefix(pm, "pnpm"):
				return PMPnpm
			case strings.HasPrefix(pm, "yarn"):
				return PMYarn
			case strings.HasPrefix(pm, "npm"):
				return PMNpm
			case strings.HasPrefix(pm, "bun"):
				return PMBun
			}
		}
	}
	// Lockfile precedence: more specific wins.
	switch {
	case exists(cwd, "bun.lockb"), exists(cwd, "bun.lock"):
		return PMBun
	case exists(cwd, "pnpm-lock.yaml"):
		return PMPnpm
	case exists(cwd, "yarn.lock"):
		return PMYarn
	case exists(cwd, "package-lock.json"):
		return PMNpm
	default:
		return PMNpm // safest default
	}
}

func detectRust() LangSection {
	return LangSection{
		Language: LangRust,
		Manager:  PMCargo,
		Commands: map[string]string{
			"build":  "cargo build",
			"test":   "cargo test",
			"clippy": "cargo clippy --all-targets",
			"format": "cargo fmt",
			"run":    "cargo run",
		},
	}
}

func detectPython(cwd string) LangSection {
	pm := detectPythonPM(cwd)
	cmds := map[string]string{}
	// pytest is the most common runner; we recommend it but defer
	// to the user when they don't have it installed.
	cmds["test"] = "pytest"
	// Linting: ruff is the de-facto modern choice; flake8/pylint
	// users will edit the doc.
	cmds["lint"] = "ruff check ."
	cmds["format"] = "ruff format ."
	if pm == PMUv {
		cmds["install"] = "uv sync"
	} else if pm == PMPoetry {
		cmds["install"] = "poetry install"
	} else {
		cmds["install"] = "pip install -r requirements.txt"
	}
	return LangSection{
		Language: LangPython,
		Manager:  pm,
		Commands: cmds,
	}
}

func detectPythonPM(cwd string) PackageManager {
	if exists(cwd, "uv.lock") {
		return PMUv
	}
	if exists(cwd, "poetry.lock") {
		return PMPoetry
	}
	return PMPip
}

func javaSection(pm PackageManager) LangSection {
	if pm == PMGradle {
		return LangSection{
			Language: LangJava, Manager: PMGradle,
			Commands: map[string]string{
				"build": "./gradlew build",
				"test":  "./gradlew test",
			},
		}
	}
	return LangSection{
		Language: LangJava, Manager: PMMaven,
		Commands: map[string]string{
			"build": "mvn package",
			"test":  "mvn test",
		},
	}
}

func rubySection() LangSection {
	return LangSection{
		Language: LangRuby,
		Commands: map[string]string{
			"install": "bundle install",
			"test":    "bundle exec rspec",
		},
	}
}

func phpSection() LangSection {
	return LangSection{
		Language: LangPHP,
		Commands: map[string]string{
			"install": "composer install",
			"test":    "composer test",
		},
	}
}

// sniffNotes returns ad-hoc observations the renderer prints under
// the "Detected notes" section. Each note describes something a new
// contributor should know but biu can't fully encode.
func sniffNotes(cwd string) []string {
	var notes []string
	if exists(cwd, "Makefile") {
		notes = append(notes, "Makefile present — run `make` to see available targets.")
	}
	if exists(cwd, ".cursor/rules") || exists(cwd, ".cursorrules") {
		notes = append(notes, "Cursor rules detected — consider importing them under a `## Cursor rules` heading.")
	}
	if exists(cwd, ".github/copilot-instructions.md") {
		notes = append(notes, "Copilot instructions detected at `.github/copilot-instructions.md` — fold the relevant parts in.")
	}
	if exists(cwd, "AGENTS.md") {
		notes = append(notes, "AGENTS.md present — consider linking or copying its content here.")
	}
	if exists(cwd, ".env.example") {
		notes = append(notes, "`.env.example` found — list required env vars under `## Setup`.")
	}
	if exists(cwd, "docker-compose.yml") || exists(cwd, "docker-compose.yaml") {
		notes = append(notes, "docker-compose detected — document `docker compose up` flow.")
	}
	if exists(cwd, "README.md") {
		notes = append(notes, "README.md present — pull non-obvious build / setup notes from it.")
	}
	return notes
}

// exists reports whether `cwd/name` is a file or directory. Non-
// existence (the common case) is a fast errno; we never read the
// content here.
func exists(cwd, name string) bool {
	_, err := os.Stat(filepath.Join(cwd, name))
	return err == nil
}

// ─── render ──────────────────────────────────────────────

// Render produces the BIUMIND.md body. Designed to be edit-friendly:
// every section has a short prompt-like placeholder so the user
// knows what biu expects to see. Keeps headings stable so a future
// re-detection can patch in place rather than rewrite.
func (d DetectedProject) Render() string {
	var b strings.Builder
	b.WriteString("# BIUMIND.md\n\n")
	b.WriteString("This file gives biu (and the model) context that persists across sessions. ")
	b.WriteString("Keep it short — only what the model would get wrong without it.\n\n")

	if len(d.Languages) == 0 {
		b.WriteString("## Project type\n\nbiu didn't recognise a manifest. " +
			"Add the language(s) and the build / test commands here.\n\n")
	} else {
		b.WriteString("## Project type\n\nDetected: ")
		names := make([]string, 0, len(d.Languages))
		for _, l := range d.Languages {
			names = append(names, string(l.Language))
		}
		b.WriteString(strings.Join(names, ", "))
		b.WriteString(".\n\n")

		b.WriteString("## Commands\n\n")
		for _, l := range d.Languages {
			fmt.Fprintf(&b, "### %s", l.Language)
			if l.Manager != "" {
				fmt.Fprintf(&b, " (%s)", l.Manager)
			}
			b.WriteString("\n\n")
			intents := sortedKeys(l.Commands)
			for _, k := range intents {
				if v := strings.TrimSpace(l.Commands[k]); v != "" {
					fmt.Fprintf(&b, "- **%s** — `%s`\n", k, v)
				}
			}
			b.WriteByte('\n')
		}
	}

	if len(d.Notes) > 0 {
		b.WriteString("## Detected notes\n\n")
		for _, n := range d.Notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteByte('\n')
	}

	b.WriteString(`## Architecture

Add 1–3 paragraphs the model would get wrong without them — surprising
seams, request flow, ownership boundaries. Skip the obvious.

## Conventions

Coding-style preferences specific to this project (e.g. "no test mocks
for DB integration", "every public Go func has a one-line doc
comment").

## Gotchas

Environmental quirks, required env vars, deploy caveats — anything
that bites a fresh contributor on day one.
`)
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
