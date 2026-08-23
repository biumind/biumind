// Stack detection and binary probing for repo-app.
//
// Stack classification is a thin wrapper over projectinit.Detect (which
// already implements the Node package-manager five-way decision and the
// Python uv/poetry/pip decision); this file adds what repo-app needs on
// top: version requirements read from the repo, a runnable start
// command, and binary probing (system → managed → candidate dirs) in
// the style of biumindkit/code/agent/detect.go.

package repoapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/projectinit"
	toml "github.com/pelletier/go-toml/v2"
)

// Stack is the runnable kind of a repo.
type Stack string

const (
	StackNode    Stack = "node"
	StackPython  Stack = "python"
	StackDocker  Stack = "docker"
	StackUnknown Stack = "unknown"
)

// StackPlan is everything install/run needs to know about a repo: what
// kind of stack it is, which package manager, declared runtime version
// requirements, and the command that starts the web service.
type StackPlan struct {
	Stack          Stack
	PackageManager string // projectinit.PackageManager value; empty for docker
	NodeReq        string // raw requirement: .nvmrc / .node-version / engines.node
	PythonReq      string // raw requirement: requires-python / .python-version
	Entry          string // human-readable entry point (for progress output)
	StartCmd       string // run via `sh -c`; may reference $PORT
}

// PlanStack classifies repoDir and derives a start command. Precedence
// is web-service oriented: a Node project with a start/dev script wins,
// then a Python project with a recognisable entry file, then a bare
// Dockerfile. slug is only used to name the docker image.
func PlanStack(repoDir, slug string) (StackPlan, error) {
	det := projectinit.Detect(repoDir)
	reqs := ReadRequirements(repoDir)

	var node *projectinit.LangSection
	var python *projectinit.LangSection
	for i := range det.Languages {
		switch det.Languages[i].Language {
		case projectinit.LangNode:
			node = &det.Languages[i]
		case projectinit.LangPython:
			python = &det.Languages[i]
		}
	}

	if node != nil {
		plan := StackPlan{
			Stack:          StackNode,
			PackageManager: string(node.Manager),
			NodeReq:        reqs.Node,
		}
		// projectinit already surfaced scripts as "<pm> run <intent>".
		if cmd := node.Commands["start"]; cmd != "" {
			plan.StartCmd = cmd
			plan.Entry = "scripts.start"
		} else if cmd := node.Commands["dev"]; cmd != "" {
			plan.StartCmd = cmd
			plan.Entry = "scripts.dev"
		} else {
			return plan, fmt.Errorf("node project has no start/dev script in package.json — add one, or set start_cmd in runtime.json by hand")
		}
		return plan, nil
	}

	if python != nil {
		plan := StackPlan{
			Stack:          StackPython,
			PackageManager: string(python.Manager),
			PythonReq:      reqs.Python,
		}
		for _, cand := range []string{"app.py", "main.py", "server.py", "run.py", "wsgi.py", "manage.py"} {
			if fileExists(filepath.Join(repoDir, cand)) {
				// The project venv's bin dir lands on PATH via
				// runtime.json path_extra, so plain `python` resolves.
				plan.StartCmd = "python " + cand
				plan.Entry = cand
				return plan, nil
			}
		}
		return plan, fmt.Errorf("python project has no recognisable entry file (app.py/main.py/server.py/...) — set start_cmd in runtime.json by hand")
	}

	if fileExists(filepath.Join(repoDir, "Dockerfile")) {
		return StackPlan{
			Stack: StackDocker,
			// The app inside the container is expected to honour $PORT;
			// sh -c expands ${PORT} from the runner-injected env.
			StartCmd: fmt.Sprintf("docker run --rm -p 127.0.0.1:${PORT}:${PORT} -e PORT repoapp-%s", slug),
			Entry:    "Dockerfile",
		}, nil
	}

	return StackPlan{Stack: StackUnknown}, fmt.Errorf("unsupported project: no package.json, python manifest, or Dockerfile found")
}

// Requirements carries the runtime version constraints a repo declares.
type Requirements struct {
	Node   string
	Python string
}

// ReadRequirements pulls version declarations from the usual files.
// Precedence: dedicated version files beat manifest ranges.
func ReadRequirements(repoDir string) Requirements {
	var r Requirements
	if v := readFirstLine(filepath.Join(repoDir, ".nvmrc")); v != "" {
		r.Node = v
	} else if v := readFirstLine(filepath.Join(repoDir, ".node-version")); v != "" {
		r.Node = v
	} else if raw, err := os.ReadFile(filepath.Join(repoDir, "package.json")); err == nil {
		var doc struct {
			Engines struct {
				Node string `json:"node"`
			} `json:"engines"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			r.Node = strings.TrimSpace(doc.Engines.Node)
		}
	}
	if v := readFirstLine(filepath.Join(repoDir, ".python-version")); v != "" {
		r.Python = v
	} else if raw, err := os.ReadFile(filepath.Join(repoDir, "pyproject.toml")); err == nil {
		var doc struct {
			Project struct {
				RequiresPython string `toml:"requires-python"`
			} `toml:"project"`
		}
		if toml.Unmarshal(raw, &doc) == nil {
			r.Python = strings.TrimSpace(doc.Project.RequiresPython)
		}
	}
	return r
}

func readFirstLine(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	return strings.TrimPrefix(line, "v")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ─── binary probing ───────────────────────────────────────

// ProbeSource tells where a resolved binary came from.
type ProbeSource string

const (
	SourceSystem  ProbeSource = "system"
	SourceManaged ProbeSource = "managed" // installed by biu into ~/.biumind/runtimes
	SourceMissing ProbeSource = "missing"
)

// Probe is the detection result for one binary.
type Probe struct {
	Name    string
	Path    string
	Version string // first line of `--version`; empty when probing failed
	Source  ProbeSource
}

// doctorBinaries is the fixed set `biu repo-app doctor` reports on.
var doctorBinaries = []string{"git", "python3", "uv", "node", "mise", "docker"}

// Doctor probes every runtime repo-app cares about, in report order.
func Doctor(ctx context.Context) []Probe {
	out := make([]Probe, 0, len(doctorBinaries))
	for _, name := range doctorBinaries {
		out = append(out, ProbeBinary(ctx, name))
	}
	return out
}

// ProbeBinary resolves a binary: PATH first (system), then the biu-
// managed toolchain dir, then well-known candidate locations (nvm /
// brew / .local/bin — the same list biumindkit's agent detection uses).
func ProbeBinary(ctx context.Context, name string) Probe {
	if p, err := exec.LookPath(name); err == nil {
		return Probe{Name: name, Path: p, Version: probeVersion(ctx, p), Source: SourceSystem}
	}
	// Managed single binaries (uv, mise) live in runtimes/bin.
	if dir, err := RuntimesDir(); err == nil {
		cand := filepath.Join(dir, "bin", name)
		if isExecutableFile(cand) {
			return Probe{Name: name, Path: cand, Version: probeVersion(ctx, cand), Source: SourceManaged}
		}
		// Managed toolchains install versioned trees: glob for them.
		for _, pat := range managedGlobPatterns(dir, name) {
			if matches, err := filepath.Glob(pat); err == nil {
				for _, m := range matches {
					if isExecutableFile(m) {
						return Probe{Name: name, Path: m, Version: probeVersion(ctx, m), Source: SourceManaged}
					}
				}
			}
		}
	}
	for _, dir := range candidateDirs() {
		cand := filepath.Join(dir, name)
		if isExecutableFile(cand) {
			return Probe{Name: name, Path: cand, Version: probeVersion(ctx, cand), Source: SourceSystem}
		}
	}
	return Probe{Name: name, Source: SourceMissing}
}

// managedGlobPatterns maps a binary name to the install-tree layouts the
// bootstrapper produces under runtimesDir.
func managedGlobPatterns(runtimesDir, name string) []string {
	switch name {
	case "node":
		return []string{filepath.Join(runtimesDir, "mise", "installs", "node", "*", "bin", "node")}
	case "python3":
		return []string{filepath.Join(runtimesDir, "python", "*", "bin", "python3")}
	default:
		return nil
	}
}

// candidateDirs mirrors biumindkit/code/agent/agent.go's candidateDirs:
// user-level install locations a login-shell PATH might not include.
func candidateDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
		)
		// nvm: ~/.nvm/versions/node/*/bin
		if matches, err := filepath.Glob(
			filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); err == nil {
			dirs = append(dirs, matches...)
		}
	}
	return append(dirs,
		"/opt/homebrew/bin", // Apple Silicon brew
		"/usr/local/bin",    // Intel brew / general
		"/usr/bin",
	)
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// probeVersion runs `<path> --version` with a timeout and returns the
// trimmed first line; failures yield "" (never block detection on a
// hung binary).
func probeVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line
}

// ─── version requirement matching ─────────────────────────

// versionTuple is a parsed "major.minor" (minor defaults to -1 =
// unspecified).
type versionTuple struct {
	major, minor int
}

// parseVersion extracts the first numeric version tuple from a string
// like "v20.11.0", "Python 3.11.2", "3.10".
func parseVersion(s string) (versionTuple, bool) {
	var nums []int
	cur := ""
	flush := func() {
		if cur != "" {
			if n, err := strconv.Atoi(cur); err == nil {
				nums = append(nums, n)
			}
			cur = ""
		}
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur += string(r)
		} else {
			flush()
			if len(nums) > 0 && r != '.' {
				break
			}
		}
	}
	flush()
	if len(nums) == 0 {
		return versionTuple{}, false
	}
	v := versionTuple{major: nums[0], minor: -1}
	if len(nums) > 1 {
		v.minor = nums[1]
	}
	return v, true
}

// splitClauses breaks a compound requirement (">=3.10,<3.13" /
// ">=18 || ^20") into individual clauses.
func splitClauses(req string) []string {
	req = strings.ReplaceAll(req, "||", ",")
	parts := strings.Split(req, ",")
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pythonSatisfies reports whether a found python3 version string
// satisfies a requires-python / .python-version requirement. An empty
// requirement is satisfied by any present version. Compound clauses
// must all hold. Only major/minor precision is compared — patch-level
// pinning beyond major.minor is treated as major.minor equality (M1
// simplification; uv picks a concrete interpreter at install time).
func pythonSatisfies(foundVersion, req string) bool {
	found, ok := parseVersion(foundVersion)
	if !ok {
		return false
	}
	req = strings.TrimSpace(req)
	if req == "" {
		return true
	}
	for _, clause := range splitClauses(req) {
		if !versionClauseSatisfies(found, clause) {
			return false
		}
	}
	return true
}

// nodeSatisfies reports whether a found node version string satisfies a
// .nvmrc / engines.node requirement, comparing at major precision.
func nodeSatisfies(foundVersion, req string) bool {
	found, ok := parseVersion(foundVersion)
	if !ok {
		return false
	}
	req = strings.TrimSpace(req)
	if req == "" {
		return true
	}
	for _, clause := range splitClauses(req) {
		if !versionClauseSatisfies(found, clause) {
			return false
		}
	}
	return true
}

// versionClauseSatisfies evaluates one comparator clause against a found
// version. Supported operators: >=, >, <=, <, ==/=, ^, ~, and a bare
// version (exact match at the precision given — "20" matches any 20.x,
// "3.10" matches 3.10.x). Unparseable clauses fail closed (treated as
// unsatisfied) so a weird requirement triggers a managed install rather
// than a silently wrong runtime.
func versionClauseSatisfies(found versionTuple, clause string) bool {
	clause = strings.TrimSpace(clause)
	op := "=="
	for _, candidate := range []string{">=", "<=", "==", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(clause, candidate) {
			op = candidate
			clause = strings.TrimSpace(strings.TrimPrefix(clause, candidate))
			break
		}
	}
	want, ok := parseVersion(clause)
	if !ok {
		return false
	}
	cmpMinor := want.minor >= 0 && found.minor >= 0
	cmp := 0
	switch {
	case found.major != want.major:
		if found.major > want.major {
			cmp = 1
		} else {
			cmp = -1
		}
	case cmpMinor && found.minor != want.minor:
		if found.minor > want.minor {
			cmp = 1
		} else {
			cmp = -1
		}
	}
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case "<":
		return cmp < 0
	case "^", "~":
		// Caret/tilde pin the major (node semver semantics; python
		// Poetry-style ~ is approximated the same way at M1).
		return found.major == want.major && cmp >= 0
	default: // "==", "=", bare
		return cmp == 0
	}
}
