// Stack detection: probe well-known feature files via the contents API
// in priority order and derive install/start commands + runtime
// requirements (tech plan §2.1, design doc 4.2).
//
// Priority: Dockerfile → docker-compose (rejected, D4) → package.json
// → requirements.txt / pyproject.toml → index.html (pure static) →
// unsupported.

package repoanalyze

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Stack kinds.
const (
	StackUnsupported  = "unsupported"
	StackDockerfile   = "dockerfile"
	StackNodeFrontend = "node_frontend" // has build script but no server entry — produces static assets
	StackNodeServer   = "node_server"   // has start script / server.js
	StackPython       = "python"
	StackStatic       = "static"
)

// RuntimeReq is one runtime dependency the runner must provide
// (tech plan §3.6: mise-managed, user-local).
type RuntimeReq struct {
	Name        string `json:"name"`         // "node" | "python" | "docker"
	Version     string `json:"version"`      // constraint string, e.g. ">=18"
	AutoInstall bool   `json:"auto_install"` // runner may install it via mise without asking
}

// Stack describes how to build and run the repo locally.
type Stack struct {
	Kind        string       `json:"kind"`
	InstallCmd  string       `json:"install_cmd,omitempty"`
	StartCmd    string       `json:"start_cmd,omitempty"`
	Port        int          `json:"port,omitempty"`        // expected listen port; 0 = static output
	HealthPath  string       `json:"health_path,omitempty"` // HTTP health check path
	Reason      string       `json:"reason,omitempty"`      // set when Kind == StackUnsupported
	RuntimeReqs []RuntimeReq `json:"runtime_reqs,omitempty"`
}

// Detect probes the repo at ref and classifies the stack. A
// StackUnsupported result is a nil-error return — callers show Reason
// to the user instead of treating it as a failure.
func Detect(ctx context.Context, gh *Client, owner, repo, ref string) (*Stack, error) {
	file := func(path string) ([]byte, bool, error) {
		return gh.FileContent(ctx, owner, repo, path, ref)
	}

	// 1. Dockerfile — single-container build.
	if content, ok, err := file("Dockerfile"); err != nil {
		return nil, err
	} else if ok {
		port := parseExpose(content)
		return &Stack{
			Kind:        StackDockerfile,
			InstallCmd:  "docker build -t repo-app .",
			StartCmd:    "docker run --rm -p ${PORT}:" + strconv.Itoa(port) + " repo-app",
			Port:        port,
			HealthPath:  "/",
			RuntimeReqs: []RuntimeReq{{Name: "docker", Version: "", AutoInstall: false}},
		}, nil
	}

	// 2. docker-compose — multi-container, explicitly out of scope (D4).
	for _, p := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if _, ok, err := file(p); err != nil {
			return nil, err
		} else if ok {
			return &Stack{
				Kind:   StackUnsupported,
				Reason: "检测到 docker-compose 多容器编排，Repo App 一期仅支持单进程项目（M2 再评估 compose 支持）",
			}, nil
		}
	}

	// 3. package.json — Node project.
	if content, ok, err := file("package.json"); err != nil {
		return nil, err
	} else if ok {
		return detectNode(ctx, gh, owner, repo, ref, content)
	}

	// 4. Python — requirements.txt or pyproject.toml.
	_, hasReq, err := file("requirements.txt")
	if err != nil {
		return nil, err
	}
	pyproject, hasPyproject, err := file("pyproject.toml")
	if err != nil {
		return nil, err
	}
	if hasReq || hasPyproject {
		return detectPython(ctx, gh, owner, repo, ref, hasReq, pyproject)
	}

	// 5. Pure static site.
	for _, p := range []string{"index.html", "dist/index.html"} {
		if _, ok, err := file(p); err != nil {
			return nil, err
		} else if ok {
			return &Stack{Kind: StackStatic}, nil
		}
	}

	return &Stack{
		Kind:   StackUnsupported,
		Reason: "未识别到可运行的技术栈（目前支持 Dockerfile / Node / Python / 纯静态站点）",
	}, nil
}

// ─── Node ────────────────────────────────────────────────────────────

type rawPackageJSON struct {
	Scripts        map[string]string `json:"scripts"`
	Engines        map[string]string `json:"engines"`
	PackageManager string            `json:"packageManager"` // e.g. "pnpm@9.1.0"
}

func detectNode(ctx context.Context, gh *Client, owner, repo, ref string, content []byte) (*Stack, error) {
	var pkg rawPackageJSON
	if err := json.Unmarshal(content, &pkg); err != nil {
		return nil, fmt.Errorf("%w: package.json: %v", ErrUpstreamShape, err)
	}
	pm := "npm"
	if pkg.PackageManager != "" {
		pm, _, _ = strings.Cut(pkg.PackageManager, "@")
	}
	nodeVersion := pkg.Engines["node"]
	if nodeVersion == "" {
		nodeVersion = ">=18"
	}
	reqs := []RuntimeReq{{Name: "node", Version: nodeVersion, AutoInstall: true}}

	// Server entry: scripts.start wins; bare server.js gets a direct
	// `node server.js`. Otherwise a build script means frontend build
	// type (static output, no long-running process).
	if _, hasStart := pkg.Scripts["start"]; hasStart {
		return &Stack{
			Kind:        StackNodeServer,
			InstallCmd:  pm + " install",
			StartCmd:    pm + " start",
			Port:        3000,
			HealthPath:  "/",
			RuntimeReqs: reqs,
		}, nil
	}
	if _, ok, err := gh.FileContent(ctx, owner, repo, "server.js", ref); err != nil {
		return nil, err
	} else if ok {
		return &Stack{
			Kind:        StackNodeServer,
			InstallCmd:  pm + " install",
			StartCmd:    "node server.js",
			Port:        3000,
			HealthPath:  "/",
			RuntimeReqs: reqs,
		}, nil
	}
	if _, hasBuild := pkg.Scripts["build"]; hasBuild {
		return &Stack{
			Kind:        StackNodeFrontend,
			InstallCmd:  pm + " install",
			StartCmd:    pm + " run build",
			RuntimeReqs: reqs,
		}, nil
	}
	return &Stack{
		Kind:   StackUnsupported,
		Reason: "package.json 中既没有 start/server.js 服务入口，也没有 build 脚本，无法判断如何运行",
	}, nil
}

// ─── Python ──────────────────────────────────────────────────────────

var requiresPythonRe = regexp.MustCompile(`requires-python\s*=\s*"([^"]+)"`)

func detectPython(ctx context.Context, gh *Client, owner, repo, ref string, hasReq bool, pyproject []byte) (*Stack, error) {
	version := ">=3.10"
	if m := requiresPythonRe.FindSubmatch(pyproject); m != nil {
		version = string(m[1])
	}

	installCmd := "pip install ."
	if hasReq {
		installCmd = "pip install -r requirements.txt"
	}
	// A Makefile with a `setup:` target is the project's own bootstrap
	// contract — prefer it over guessing.
	if mk, ok, err := gh.FileContent(ctx, owner, repo, "Makefile", ref); err != nil {
		return nil, err
	} else if ok && hasMakeTarget(mk, "setup") {
		installCmd = "make setup"
	}

	return &Stack{
		Kind:        StackPython,
		InstallCmd:  installCmd,
		Port:        8000,
		HealthPath:  "/",
		RuntimeReqs: []RuntimeReq{{Name: "python", Version: version, AutoInstall: true}},
	}, nil
}

// hasMakeTarget reports whether the Makefile defines a target line
// `name:` (allowing prerequisites after the colon).
func hasMakeTarget(content []byte, name string) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, name+":") {
			return true
		}
	}
	return false
}

// ─── misc ────────────────────────────────────────────────────────────

var exposeRe = regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d+)`)

func parseExpose(content []byte) int {
	m := exposeRe.FindSubmatch(content)
	if m == nil {
		return 8080
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 8080
	}
	return n
}
