// Runtime bootstrap: when the host lacks a required toolchain, install
// it user-locally under ~/.biumind/runtimes/ — never touching the
// system. Python comes via the uv single binary (`uv python install` +
// per-instance `uv venv`), Node via the mise single binary
// (`mise install node@<ver>`). Docker is detected but never installed —
// Docker Desktop is far too heavy to auto-fetch; the user gets a clear
// error instead.

package repoapp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// BootstrapPlan is the pure decision output of DecideBootstrap — what
// must be downloaded / installed before dependencies can be resolved.
// Kept side-effect free so the decision matrix is unit-testable.
type BootstrapPlan struct {
	DownloadUV    bool   // fetch the uv single binary into runtimes/bin
	DownloadMise  bool   // fetch the mise single binary into runtimes/bin
	InstallPython bool   // run `uv python install` (system python missing/unsuitable)
	PythonInstall string // version for `uv python install` ("" = uv's default)
	NodeInstall   string // version for `mise install node@<ver>` ("" = none needed)
	DockerMissing bool   // caller must surface the "install Docker" error
	Notes         []string
}

// DecideBootstrap maps (stack plan × probed binaries) to the minimal
// bootstrap work. probes is keyed by binary name ("python3", "uv",
// "node", "mise", "docker") as returned by ProbeBinary.
func DecideBootstrap(sp StackPlan, probes map[string]Probe) BootstrapPlan {
	var plan BootstrapPlan
	probe := func(name string) Probe {
		if p, ok := probes[name]; ok {
			return p
		}
		return Probe{Name: name, Source: SourceMissing}
	}

	switch sp.Stack {
	case StackDocker:
		if probe("docker").Source == SourceMissing {
			plan.DockerMissing = true
		}
	case StackPython:
		py := probe("python3")
		if py.Source == SourceMissing || !pythonSatisfies(py.Version, sp.PythonReq) {
			if probe("uv").Source == SourceMissing {
				plan.DownloadUV = true
			}
			plan.InstallPython = true
			plan.PythonInstall = pythonInstallVersion(sp.PythonReq)
			plan.Notes = append(plan.Notes, noteFor("python3", py, sp.PythonReq))
		}
	case StackNode:
		nd := probe("node")
		if nd.Source == SourceMissing || !nodeSatisfies(nd.Version, sp.NodeReq) {
			if probe("mise").Source == SourceMissing {
				plan.DownloadMise = true
			}
			plan.NodeInstall = nodeInstallVersion(sp.NodeReq)
			plan.Notes = append(plan.Notes, noteFor("node", nd, sp.NodeReq))
		}
	}
	return plan
}

func noteFor(name string, p Probe, req string) string {
	if p.Source == SourceMissing {
		return fmt.Sprintf("%s not found (need %s) — will install a managed toolchain", name, orAny(req))
	}
	return fmt.Sprintf("%s %s does not satisfy %q — will install a managed toolchain", name, p.Version, req)
}

func orAny(req string) string {
	if req == "" {
		return "any version"
	}
	return req
}

// pythonInstallVersion turns a requirement into the argument for
// `uv python install`: ">=3.10" → "3.10"; "" → "" (uv picks its
// default latest stable).
func pythonInstallVersion(req string) string {
	for _, clause := range splitClauses(req) {
		clause = strings.TrimLeft(clause, ">=<~^= ")
		if v, ok := parseVersion(clause); ok {
			if v.minor >= 0 {
				return fmt.Sprintf("%d.%d", v.major, v.minor)
			}
			return fmt.Sprintf("%d", v.major)
		}
	}
	return ""
}

// nodeInstallVersion turns a requirement into the argument for
// `mise install node@<ver>`: "^20" → "20"; "" → "lts".
func nodeInstallVersion(req string) string {
	for _, clause := range splitClauses(req) {
		clause = strings.TrimLeft(clause, ">=<~^= v")
		if v, ok := parseVersion(clause); ok {
			return fmt.Sprintf("%d", v.major)
		}
	}
	return "lts"
}

// Bootstrap executes a BootstrapPlan: downloads the single binaries,
// installs the language runtimes, and returns the PATH entries the
// managed toolchains landed in (callers persist them into runtime.json
// path_extra so the runner can find them again).
func Bootstrap(ctx context.Context, plan BootstrapPlan, logf func(format string, args ...any)) ([]string, error) {
	runtimesDir, err := RuntimesDir()
	if err != nil {
		return nil, err
	}
	binDir := filepath.Join(runtimesDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil, err
	}

	if plan.DownloadUV {
		logf("downloading uv ...")
		if err := downloadUV(ctx, filepath.Join(binDir, "uv")); err != nil {
			return nil, bootstrapErr("uv", err)
		}
	}
	if plan.DownloadMise {
		logf("downloading mise ...")
		if err := downloadMise(ctx, filepath.Join(binDir, "mise")); err != nil {
			return nil, bootstrapErr("mise", err)
		}
	}

	if plan.InstallPython {
		ver := plan.PythonInstall // "" = uv's default python
		logf("installing python %s via uv ...", orAny(ver))
		args := []string{"python", "install"}
		if ver != "" {
			args = append(args, ver)
		}
		env := append(os.Environ(),
			"UV_PYTHON_INSTALL_DIR="+filepath.Join(runtimesDir, "python"),
		)
		if err := runLogged(ctx, "", env, resolveBin(ctx, binDir, "uv"), args...); err != nil {
			return nil, bootstrapErr("uv python install", err)
		}
	}
	if plan.NodeInstall != "" {
		logf("installing node@%s via mise ...", plan.NodeInstall)
		env := append(os.Environ(),
			"MISE_DATA_DIR="+filepath.Join(runtimesDir, "mise"),
			// Non-interactive: never prompt for plugin trust / confirmations.
			"MISE_YES=1",
		)
		if err := runLogged(ctx, "", env, resolveBin(ctx, binDir, "mise"),
			"install", "node@"+plan.NodeInstall); err != nil {
			return nil, bootstrapErr("mise install node", err)
		}
	}

	// Collect the bin dirs of everything we just installed.
	var extra []string
	seen := map[string]bool{}
	for _, name := range []string{"node", "python3"} {
		for _, pat := range managedGlobPatterns(runtimesDir, name) {
			if matches, err := filepath.Glob(pat); err == nil {
				for _, m := range matches {
					d := filepath.Dir(m)
					if !seen[d] {
						seen[d] = true
						extra = append(extra, d)
					}
				}
			}
		}
	}
	return extra, nil
}

func bootstrapErr(what string, err error) error {
	return fmt.Errorf("bootstrap %s failed: %w — run `biu repo-app doctor` to inspect your runtimes", what, err)
}

// resolveBin prefers the managed single binary in binDir (freshly
// downloaded), falling back to a probed system/managed install.
func resolveBin(ctx context.Context, binDir, name string) string {
	cand := filepath.Join(binDir, name)
	if isExecutableFile(cand) {
		return cand
	}
	if p := ProbeBinary(ctx, name); p.Source != SourceMissing {
		return p.Path
	}
	return cand // let exec fail with a clear not-found error
}

// runLogged runs a command, echoing combined output only on failure.
func runLogged(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// uvAsset maps the current platform to the uv release asset name.
// Mirrors https://astral.sh/uv release artifacts.
func uvAsset() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "uv-aarch64-apple-darwin.tar.gz", nil
	case "darwin/amd64":
		return "uv-x86_64-apple-darwin.tar.gz", nil
	case "linux/amd64":
		return "uv-x86_64-unknown-linux-gnu.tar.gz", nil
	case "linux/arm64":
		return "uv-aarch64-unknown-linux-gnu.tar.gz", nil
	default:
		return "", fmt.Errorf("no prebuilt uv binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// downloadUV fetches the latest uv release tarball from GitHub and
// extracts the `uv` binary to dest (mode 0755).
func downloadUV(ctx context.Context, dest string) error {
	asset, err := uvAsset()
	if err != nil {
		return err
	}
	url := "https://github.com/astral-sh/uv/releases/latest/download/" + asset
	body, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	gz, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("gunzip uv asset: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read uv asset tar: %w", err)
		}
		// The archive ships <target-dir>/uv (+ uvx); we only need uv.
		if filepath.Base(hdr.Name) != "uv" || hdr.FileInfo().IsDir() {
			continue
		}
		return writeExecutable(dest, tr)
	}
	return fmt.Errorf("uv binary not found inside %s", asset)
}

// miseAsset maps the current platform to mise's stable latest-binary URL
// path (https://mise.jdx.dev/mise-latest-<platform>).
func miseAsset() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "mise-latest-macos-arm64", nil
	case "darwin/amd64":
		return "mise-latest-macos-x64", nil
	case "linux/amd64":
		return "mise-latest-linux-x64", nil
	case "linux/arm64":
		return "mise-latest-linux-arm64", nil
	default:
		return "", fmt.Errorf("no prebuilt mise binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// downloadMise fetches the raw mise binary to dest (mode 0755).
func downloadMise(ctx context.Context, dest string) error {
	asset, err := miseAsset()
	if err != nil {
		return err
	}
	body, err := httpGet(ctx, "https://mise.jdx.dev/"+asset)
	if err != nil {
		return err
	}
	defer body.Close()
	return writeExecutable(dest, body)
}

func writeExecutable(dest string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return nil
}

// httpGet issues a GET with a generous-but-bounded timeout (toolchain
// downloads are tens of MB) and returns the response body on 200.
func httpGet(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return resp.Body, nil
}

// InstallDeps installs project dependencies into the repo checkout
// according to the stack plan. pathExtra carries managed toolchain bin
// dirs from Bootstrap. Returns additional PATH entries the runner must
// persist (the project venv, the node dir actually used, ...).
func InstallDeps(ctx context.Context, inst Instance, sp StackPlan, pathExtra []string, logf func(format string, args ...any)) ([]string, error) {
	repoDir := inst.RepoDir()
	env := envWithPathExtra(os.Environ(), pathExtra)

	switch sp.Stack {
	case StackStatic:
		// No dependencies to install — the CLI's built-in serve-static
		// server needs nothing from the repo or the system.
		logf("static site: nothing to install")
		return pathExtra, nil
	case StackPython:
		uv := ProbeBinary(ctx, "uv")
		if uv.Source == SourceMissing {
			return nil, fmt.Errorf("uv not found — bootstrap should have installed it; run `biu repo-app doctor`")
		}
		logf("creating virtualenv (uv venv) ...")
		if err := runLogged(ctx, repoDir, env, uv.Path, "venv"); err != nil {
			return nil, err
		}
		switch {
		case fileExists(filepath.Join(repoDir, "requirements.txt")):
			logf("installing requirements.txt (uv pip) ...")
			if err := runLogged(ctx, repoDir, env, uv.Path, "pip", "install", "-r", "requirements.txt"); err != nil {
				return nil, err
			}
		case fileExists(filepath.Join(repoDir, "pyproject.toml")):
			logf("installing project (uv pip install .) ...")
			if err := runLogged(ctx, repoDir, env, uv.Path, "pip", "install", "."); err != nil {
				return nil, err
			}
		}
		// uv venv created .venv inside the repo; its bin dir goes on PATH
		// so the start command's bare `python` resolves to it.
		return []string{filepath.Join(repoDir, ".venv", "bin")}, nil

	case StackNode:
		pm := sp.PackageManager
		if pm == "" {
			pm = "npm"
		}
		// Non-npm package managers may be absent even when node exists;
		// corepack (bundled with node) can provision them user-locally.
		if pm != "npm" && ProbeBinary(ctx, pm).Source == SourceMissing {
			if corepack := probeInPath(env, "corepack"); corepack != "" {
				logf("enabling %s via corepack ...", pm)
				// Best-effort: a failure here surfaces again at install.
				_ = runLogged(ctx, repoDir, env, corepack, "prepare", pm+"@latest", "--activate")
			}
		}
		logf("installing node dependencies (%s install) ...", pm)
		if err := runLogged(ctx, repoDir, env, pm, "install"); err != nil {
			return nil, fmt.Errorf("%s install failed: %w — check the repo's lockfile, or run it by hand in %s", pm, err, repoDir)
		}
		return nil, nil

	case StackDocker:
		tag := "repoapp-" + inst.Slug
		logf("building docker image %s ...", tag)
		if err := runLogged(ctx, repoDir, env, "docker", "build", "-t", tag, "."); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return nil, fmt.Errorf("cannot install dependencies for stack %q", sp.Stack)
}

// envWithPathExtra returns env with the dirs prepended to PATH.
func envWithPathExtra(env []string, extra []string) []string {
	if len(extra) == 0 {
		return env
	}
	prefix := strings.Join(extra, string(os.PathListSeparator))
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + prefix + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
			return env
		}
	}
	return append(env, "PATH="+prefix)
}

// probeInPath resolves name against the PATH inside env (not the
// process PATH) — used to find corepack among managed node installs.
func probeInPath(env []string, name string) string {
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			continue
		}
		for _, dir := range filepath.SplitList(strings.TrimPrefix(kv, "PATH=")) {
			cand := filepath.Join(dir, name)
			if isExecutableFile(cand) {
				return cand
			}
		}
	}
	return ""
}
